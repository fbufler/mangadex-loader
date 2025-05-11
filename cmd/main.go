package main

import (
	"log/slog"
	"net/url"
	"os"
	"time"

	"github.com/fbufler/mangadex/pkg/mangadex"
	"github.com/spf13/cobra"
)

const mangadexAPIURL = "https://api.mangadex.org"

var rootCmd = &cobra.Command{
	Use:   "mangadex",
	Short: "A command line tool for Mangadex",
	Long:  `Mangadex is a command line tool for Mangadex. It allows you to search, download, and manage manga from Mangadex.`,
	Run: func(cmd *cobra.Command, args []string) {
		// This is the root command. It doesn't do anything by itself.
	},
}

var getCmd = &cobra.Command{
	Use:   "get",
	Short: "Get manga from Mangadex",
	Long:  `Get manga from Mangadex. It allows you to download manga chapters from Mangadex.`,
	Run: func(cmd *cobra.Command, args []string) {
		manga, _ := cmd.Flags().GetString("manga")
		output, _ := cmd.Flags().GetString("output")
		name, _ := cmd.Flags().GetString("name")

		apiUrl, err := url.Parse(mangadexAPIURL)
		if err != nil {
			slog.Error("Failed to parse base URL", "error", err)
			return
		}

		cfg := &mangadex.Config{
			APIUrl:        apiUrl,
			Timeout:       time.Second * 10,
			MangaID:       manga,
			MangaLanguage: mangadex.MangaLanguageEN,
			Output:        output,
			Name:          name,
			Logger:        slog.New(slog.NewTextHandler(os.Stdout, nil)),
		}

		mangadexClient := mangadex.New(cfg)
		if err := mangadexClient.DownloadManga(); err != nil {
			slog.Error("Failed to download manga", "error", err)
			return
		}
		slog.Info("Manga downloaded successfully", "output", output)
	},
}

func init() {
	// Add the get command to the root command
	rootCmd.AddCommand(getCmd)

	// Add flags to the get command
	getCmd.Flags().StringP("manga", "m", "", "The Manga ID to download")
	getCmd.Flags().StringP("output", "o", "", "The output directory to save the manga")
	getCmd.Flags().StringP("name", "n", "", "The name of the manga to download")
}

func main() {
	// Execute the root command
	if err := rootCmd.Execute(); err != nil {
		// If there is an error, print it and exit
		panic(err)
	}
}
