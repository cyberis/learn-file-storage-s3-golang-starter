package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func (cfg apiConfig) ensureAssetsDir() error {
	if _, err := os.Stat(cfg.assetsRoot); os.IsNotExist(err) {
		return os.Mkdir(cfg.assetsRoot, 0755)
	}
	return nil
}

func mediaTypeToExt(mediaType string) (string, error) {
	switch mediaType {
	case "image/jpeg":
		return ".jpg", nil
	case "image/png":
		return ".png", nil
	case "image/gif":
		return ".gif", nil
	case "video/mp4":
		return ".mp4", nil
	default:
		return "", fmt.Errorf("unsupported media type: %s", mediaType)
	}
}

func getAssetFilename(mediaType string) (string, error) {
	base := make([]byte, 32)
	_, err := rand.Read(base)
	if err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %v", err)
	}
	id := base64.RawURLEncoding.EncodeToString(base)

	ext, err := mediaTypeToExt(mediaType)
	if err != nil {
		return "", fmt.Errorf("failed to determine file extension: %v", err)
	}
	return fmt.Sprintf("%s%s", id, ext), nil
}

func (cfg apiConfig) getAssetDiskPath(assetFilename string) string {
	return filepath.Join(cfg.assetsRoot, assetFilename)
}

func (cfg apiConfig) getAssetURL(r *http.Request, assetFilename string) string {
	log.Printf("Generating URL for asset %s on platform: %s", assetFilename, cfg.platform)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/assets/%s", scheme, r.Host, assetFilename)
}

func (cfg apiConfig) getS3AssetURL(assetFilename string) string {
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, assetFilename)
}
