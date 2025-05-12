package mangadex

import (
	"crypto/tls"
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

	"github.com/fbufler/mangadex/pkg/cbz"
	"github.com/schollz/progressbar/v3"
)

type MangaLanguage string

const (
	// MangaLanguageEN is the English language code
	MangaLanguageEN MangaLanguage = "en"
	// MangaLanguageDE is the German language code
	MangaLanguageDE MangaLanguage = "de"
)

const RETRY_WAIT_TIME = 10 * time.Second

type Config struct {
	APIUrl        *url.URL
	Timeout       time.Duration
	Retries       int
	MangaID       string
	MangaLanguage MangaLanguage
	Output        string
	Name          string
	Logger        *slog.Logger
	Volume        string
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
		Transport: &http.Transport{
			TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper),
		},
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
	resp, err := c.makeRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch chapter metadata: %w", err)
	}
	defer resp.Body.Close()

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
		if c.Config.Volume != "" && safeVolume != c.Config.Volume {
			c.logger.Info("Skipping chapter not in requested volume", "chapter", id, "volume", safeVolume)
			continue
		}
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
		err := c.compressVolumeToCBZ(volume, dirs, outPath)
		if err != nil {
			return fmt.Errorf("failed to compress volume %s: %w", volume, err)
		}
		c.logger.Info("Volume compressed", "volume", volume, "file", filename)
		err = c.removeTempDirectories(dirs)
		if err != nil {
			return fmt.Errorf("failed to remove temp directories for volume %s: %w", volume, err)
		}
		c.logger.Info("Removed temp directories", "volume", volume)
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
		volumeParam := ""
		if c.Config.Volume != "" {
			volumeParam = "&volume=" + url.QueryEscape(c.Config.Volume)
		}
		apiURL := fmt.Sprintf("%s/chapter?manga=%s&translatedLanguage[]=%s%s&limit=%d&offset=%d&order[chapter]=asc",
			c.Config.APIUrl.String(), mangaID, lang, volumeParam, limit, offset)
		resp, err := c.makeRequest("GET", apiURL, nil)

		if err != nil {
			return nil, fmt.Errorf("failed to fetch chapters: %w", err)
		}
		defer resp.Body.Close()

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
	resp, err := c.makeRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch at-home API: %w", err)
	}
	defer resp.Body.Close()

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
	resp, err := c.makeRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

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

func (c *Client) compressVolumeToCBZ(volumeName string, chapterDirectories []string, outPath string) error {
	c.logger.Debug("Compressing to CBZ", "volumeName", volumeName, "outPath", outPath)
	cbzFile, err := cbz.Open(outPath, c.logger)
	if err != nil {
		return fmt.Errorf("failed to open CBZ file: %w", err)
	}

	for _, dir := range chapterDirectories {
		err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				file, err := os.Open(path)
				if err != nil {
					return fmt.Errorf("failed to open file %s: %w", path, err)
				}
				cbzFile.Add(file)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return cbzFile.Write(&cbz.WriteOptions{Order: true})
}

func (c *Client) removeTempDirectories(chapterDirectories []string) error {
	for _, dir := range chapterDirectories {
		err := os.RemoveAll(dir)
		if err != nil {
			return fmt.Errorf("failed to remove temp directory %s: %w", dir, err)
		}
	}
	return nil
}

func (c *Client) makeRequest(method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.logger.Debug("Making request", "method", method, "url", url)
	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// Handle rate limiting
		if resp.StatusCode == http.StatusTooManyRequests {
			c.logger.Warn("Rate limit reached, waiting 5 seconds")
			time.Sleep(5 * time.Second)
			return c.retryRequest(req, c.Config.Retries)
		}
		return nil, fmt.Errorf("request failed with status %s", resp.Status)
	}
	return resp, nil
}

func (c *Client) retryRequest(req *http.Request, maxRetries int) (*http.Response, error) {
	var resp *http.Response
	var err error

	for i := range maxRetries {
		resp, err = c.HttpClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		c.logger.Warn("Request failed, retrying", "attempt", i+1, "error", err)
		time.Sleep(RETRY_WAIT_TIME)
	}

	return nil, fmt.Errorf("request failed after %d attempts: %w", maxRetries, err)
}
