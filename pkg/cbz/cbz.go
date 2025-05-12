package cbz

import (
	"archive/zip"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

type CBZ struct {
	files      []*os.File
	outputPath string
	logger     *slog.Logger
}

func Open(filePath string, logger *slog.Logger) (*CBZ, error) {
	outputPath := filePath
	cbz := &CBZ{
		files:      []*os.File{},
		outputPath: outputPath,
		logger:     logger,
	}
	if err := cbz.ensureOutputPath(); err != nil {
		return nil, err
	}
	return cbz, nil
}

func (cbz *CBZ) Add(file *os.File) {
	cbz.logger.Debug("Adding file to CBZ", "fileName", file.Name())
	cbz.files = append(cbz.files, file)
}

type WriteOptions struct {
	Order bool
}

func (cbz *CBZ) Write(options *WriteOptions) error {
	cbz.logger.Debug("Writing CBZ file", "outputPath", cbz.outputPath)
	outputFile, err := os.Create(cbz.outputPath)
	if err != nil {
		return err
	}
	defer outputFile.Close()
	zipWriter := zip.NewWriter(outputFile)
	defer zipWriter.Close()
	for i, file := range cbz.files {
		cbz.logger.Debug("Adding file to CBZ", "fileName", file.Name())
		zipName := file.Name()
		if options.Order {
			zipName = fmt.Sprintf("%03d_%s", i, file.Name())
		}
		writer, err := zipWriter.Create(zipName)
		if err != nil {
			return err
		}
		_, err = io.Copy(writer, file)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	if err := zipWriter.Close(); err != nil {
		return err
	}
	if err := outputFile.Close(); err != nil {
		return err
	}
	cbz.logger.Debug("CBZ file written successfully", "outputPath", cbz.outputPath)
	return nil
}

func (cbz *CBZ) ensureOutputPath() error {
	cbz.logger.Debug("Ensuring output path exists", "outputPath", cbz.outputPath)
	if err := os.MkdirAll(filepath.Dir(cbz.outputPath), os.ModePerm); err != nil {
		return err
	}
	return nil
}
