package mangadex

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/schollz/progressbar/v3"
)

type MangaLanguage string

const (
	// MangaLanguageEN is the English language code
	MangaLanguageEN MangaLanguage = "en"
)

type Config struct {
	APIUrl        *url.URL
	Timeout       time.Duration
	MangaID       string
	MangaLanguage MangaLanguage
	Output        string
	Name          string
	Logger        *slog.Logger
}

type Client struct {
	Config     *Config
	logger     *slog.Logger
	HttpClient *http.Client
}

func New(config *Config) *Client {
	if config == nil {
		panic("Config cannot be nil")
	}
	if config.APIUrl.String() == "" {
		panic("APIUrl cannot be empty")
	}
	if config.Timeout <= 0 {
		panic("Timeout must be greater than 0")
	}
	if config.MangaID == "" {
		panic("MangaID must be provided")
	}
	if config.MangaLanguage == "" {
		panic("MangaLanguage cannot be empty")
	}
	if config.Output == "" {
		panic("Output cannot be empty")
	}
	if config.Name == "" {
		panic("Name cannot be empty")
	}
	httpClient := &http.Client{
		Timeout: config.Timeout,
	}
	return &Client{
		Config:     config,
		HttpClient: httpClient,
		logger:     config.Logger,
	}
}

type ChapterImageData struct {
	BaseURL string
	Hash    string
	Data    []string
}

type ChapterListResponse struct {
	Result   string `json:"result"`
	Response string `json:"response"`
	Data     []struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Volume             string `json:"volume"`
			Chapter            string `json:"chapter"`
			Title              string `json:"title"`
			TranslatedLanguage string `json:"translatedLanguage"`
			PublishAt          string `json:"publishAt"`
			CreatedAt          string `json:"createdAt"`
			Pages              int    `json:"pages"`
		} `json:"attributes"`
	} `json:"data"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	Total  int `json:"total"`
}

type ChapterMetadata struct {
	ID      string
	Volume  string
	Chapter string
	Title   string
}

func (c *Client) fetchChapterMetadata(chapterID string) (*ChapterMetadata, error) {
	apiURL := fmt.Sprintf("%s/chapter/%s", c.Config.APIUrl.String(), chapterID)
	resp, err := c.HttpClient.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch chapter metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chapter metadata API returned status %s", resp.Status)
	}

	var chapterResp struct {
		Result string `json:"result"`
		Data   struct {
			ID         string `json:"id"`
			Attributes struct {
				Volume  string `json:"volume"`
				Chapter string `json:"chapter"`
				Title   string `json:"title"`
			} `json:"attributes"`
		} `json:"data"`
	}
	err = json.NewDecoder(resp.Body).Decode(&chapterResp)
	if err != nil {
		return nil, fmt.Errorf("failed to decode chapter metadata: %w", err)
	}
	return &ChapterMetadata{
		ID:      chapterResp.Data.ID,
		Volume:  chapterResp.Data.Attributes.Volume,
		Chapter: chapterResp.Data.Attributes.Chapter,
		Title:   chapterResp.Data.Attributes.Title,
	}, nil
}

func (c *Client) groupChaptersByVolume(chapterIDs []string) (map[string][]string, error) {
	safe := func(s string) string {
		return stringReplaceAllRune(s, '/', '_')
	}
	volumeDirs := make(map[string][]string)
	for _, id := range chapterIDs {
		meta, err := c.fetchChapterMetadata(id)
		if err != nil {
			return nil, err
		}
		safeVolume := safe(meta.Volume)
		tmpDir, err := c.downloadChapterPages(id)
		if err != nil {
			return nil, err
		}
		volumeDirs[safeVolume] = append(volumeDirs[safeVolume], tmpDir)
		c.logger.Info("Chapter downloaded", "chapter", id, "volume", safeVolume)
	}
	return volumeDirs, nil
}

func (c *Client) DownloadManga() error {
	chapterIDs, err := c.GetChaptersByMangaID(c.Config.MangaID, "en")
	if err != nil {
		return fmt.Errorf("failed to get chapter IDs: %w", err)
	}
	volumeDirs, err := c.groupChaptersByVolume(chapterIDs)
	if err != nil {
		return err
	}
	for volume, dirs := range volumeDirs {
		filename := fmt.Sprintf("%s-volume-%s.cbz", c.Config.Name, volume)
		outPath := filepath.Join(c.Config.Output, filename)
		err := c.compressToCBZ(dirs, outPath)
		if err != nil {
			return fmt.Errorf("failed to compress volume %s: %w", volume, err)
		}
		c.logger.Info("Volume compressed", "volume", volume, "file", filename)
	}
	return nil
}

// helper to replace all runes in a string
func stringReplaceAllRune(s string, old rune, new rune) string {
	runes := []rune(s)
	for i, r := range runes {
		if r == old {
			runes[i] = new
		}
	}
	return string(runes)
}

func (c *Client) GetChaptersByMangaID(mangaID, lang string) ([]string, error) {
	var allChapterIDs []string
	limit := 100
	offset := 0

	for {
		apiURL := fmt.Sprintf("%s/chapter?manga=%s&translatedLanguage[]=%s&limit=%d&offset=%d&order[chapter]=asc",
			c.Config.APIUrl.String(), mangaID, lang, limit, offset)
		resp, err := c.HttpClient.Get(apiURL)

		if err != nil {
			return nil, fmt.Errorf("failed to fetch chapters: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("chapter API returned status %s", resp.Status)
		}

		var result ChapterListResponse

		err = json.NewDecoder(resp.Body).Decode(&result)
		if err != nil {
			return nil, fmt.Errorf("failed to decode chapter response: %w", err)
		}

		// Sort chapters by attributes.chapter
		sort.SliceStable(result.Data, func(i, j int) bool {
			// Treat empty or non-numeric chapters as greater than any number
			chapterI := result.Data[i].Attributes.Chapter
			chapterJ := result.Data[j].Attributes.Chapter

			// Try parsing as float for natural ordering like 1, 1.5, 2
			ci, errI := strconv.ParseFloat(chapterI, 64)
			cj, errJ := strconv.ParseFloat(chapterJ, 64)

			if errI == nil && errJ == nil {
				return ci < cj
			}
			if errI == nil {
				return true
			}
			if errJ == nil {
				return false
			}
			return chapterI < chapterJ
		})

		for _, item := range result.Data {
			allChapterIDs = append(allChapterIDs, item.ID)
		}

		offset += limit
		if offset >= result.Total {
			break
		}
	}

	return allChapterIDs, nil
}

func (c *Client) getChapterImageData(chapterId string) (*ChapterImageData, error) {
	c.logger.Debug("Fetching image list via At-Home API", "chapterId", chapterId)

	apiURL := fmt.Sprintf("%s/at-home/server/%s", c.Config.APIUrl.String(), chapterId)
	resp, err := c.HttpClient.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch at-home API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("at-home API returned status %s", resp.Status)
	}

	var result struct {
		BaseURL string `json:"baseUrl"`
		Chapter struct {
			Hash string   `json:"hash"`
			Data []string `json:"data"`
		} `json:"chapter"`
	}

	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return nil, fmt.Errorf("failed to decode at-home response: %w", err)
	}

	return &ChapterImageData{
		BaseURL: result.BaseURL,
		Hash:    result.Chapter.Hash,
		Data:    result.Chapter.Data,
	}, nil
}

func (c *Client) downloadImage(url, outputPath string) error {
	resp, err := c.HttpClient.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("image fetch failed with status %s", resp.Status)
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create image file: %w", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to save image: %w", err)
	}

	return nil
}

func (c *Client) downloadChapterPages(chapterId string) (string, error) {
	imageData, err := c.getChapterImageData(chapterId)
	if err != nil {
		return "", err
	}

	tmpDir, err := os.MkdirTemp("", "mangadex")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	bar := progressbar.NewOptions(len(imageData.Data),
		progressbar.OptionSetDescription("Downloading"),
		progressbar.OptionShowCount(),
		progressbar.OptionClearOnFinish(),
		progressbar.OptionSetWidth(40),
	)

	for i, file := range imageData.Data {
		imageURL := fmt.Sprintf("%s/data/%s/%s", imageData.BaseURL, imageData.Hash, file)
		c.logger.Debug("Downloading image", "url", imageURL)

		filename := fmt.Sprintf("%s/%03d_%s", tmpDir, i+1, file)
		err := c.downloadImage(imageURL, filename)
		if err != nil {
			return "", err
		}

		c.logger.Debug("Saved image", "file", filename)
		_ = bar.Add(1)
	}

	return tmpDir, nil
}

func (c *Client) compressToCBZ(tmpDirs []string, outPath string) error {
	err := c.ensureOutputDir()
	if err != nil {
		return fmt.Errorf("failed to ensure output directory: %w", err)
	}
	c.logger.Debug("Compressing to CBZ", "tmpDirs", tmpDirs, "outPath", outPath)

	cbzFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer cbzFile.Close()

	zipWriter := zip.NewWriter(cbzFile)
	defer zipWriter.Close()

	imageCounter := 1
	for _, dir := range tmpDirs {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				defer file.Close()

				ext := filepath.Ext(path)
				zipName := fmt.Sprintf("%04d%s", imageCounter, ext)

				writer, err := zipWriter.Create(zipName)
				if err != nil {
					return err
				}
				_, err = io.Copy(writer, file)
				if err != nil {
					return err
				}
				imageCounter++
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) ensureOutputDir() error {
	c.logger.Debug("Ensuring output directory exists", "output", c.Config.Output)
	if _, err := os.Stat(c.Config.Output); os.IsNotExist(err) {
		err := os.MkdirAll(c.Config.Output, 0755)
		if err != nil {
			return fmt.Errorf("failed to create output directory: %w", err)
		}
	}
	return nil
}
