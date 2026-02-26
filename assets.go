package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cyberis/learn-file-storage-s3-golang/internal/database"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type ffprobeOutput struct {
	Streams []struct {
		Index              int    `json:"index"`
		CodecName          string `json:"codec_name,omitempty"`
		CodecLongName      string `json:"codec_long_name,omitempty"`
		Profile            string `json:"profile,omitempty"`
		CodecType          string `json:"codec_type"`
		CodecTagString     string `json:"codec_tag_string"`
		CodecTag           string `json:"codec_tag"`
		Width              int    `json:"width,omitempty"`
		Height             int    `json:"height,omitempty"`
		CodedWidth         int    `json:"coded_width,omitempty"`
		CodedHeight        int    `json:"coded_height,omitempty"`
		ClosedCaptions     int    `json:"closed_captions,omitempty"`
		FilmGrain          int    `json:"film_grain,omitempty"`
		HasBFrames         int    `json:"has_b_frames,omitempty"`
		SampleAspectRatio  string `json:"sample_aspect_ratio,omitempty"`
		DisplayAspectRatio string `json:"display_aspect_ratio,omitempty"`
		PixFmt             string `json:"pix_fmt,omitempty"`
		Level              int    `json:"level,omitempty"`
		ColorRange         string `json:"color_range,omitempty"`
		ColorSpace         string `json:"color_space,omitempty"`
		ColorTransfer      string `json:"color_transfer,omitempty"`
		ColorPrimaries     string `json:"color_primaries,omitempty"`
		ChromaLocation     string `json:"chroma_location,omitempty"`
		FieldOrder         string `json:"field_order,omitempty"`
		Refs               int    `json:"refs,omitempty"`
		IsAvc              string `json:"is_avc,omitempty"`
		NalLengthSize      string `json:"nal_length_size,omitempty"`
		ID                 string `json:"id"`
		RFrameRate         string `json:"r_frame_rate"`
		AvgFrameRate       string `json:"avg_frame_rate"`
		TimeBase           string `json:"time_base"`
		StartPts           int    `json:"start_pts"`
		StartTime          string `json:"start_time"`
		DurationTs         int    `json:"duration_ts"`
		Duration           string `json:"duration"`
		BitRate            string `json:"bit_rate,omitempty"`
		BitsPerRawSample   string `json:"bits_per_raw_sample,omitempty"`
		NbFrames           string `json:"nb_frames"`
		ExtradataSize      int    `json:"extradata_size"`
		Disposition        struct {
			Default         int `json:"default"`
			Dub             int `json:"dub"`
			Original        int `json:"original"`
			Comment         int `json:"comment"`
			Lyrics          int `json:"lyrics"`
			Karaoke         int `json:"karaoke"`
			Forced          int `json:"forced"`
			HearingImpaired int `json:"hearing_impaired"`
			VisualImpaired  int `json:"visual_impaired"`
			CleanEffects    int `json:"clean_effects"`
			AttachedPic     int `json:"attached_pic"`
			TimedThumbnails int `json:"timed_thumbnails"`
			NonDiegetic     int `json:"non_diegetic"`
			Captions        int `json:"captions"`
			Descriptions    int `json:"descriptions"`
			Metadata        int `json:"metadata"`
			Dependent       int `json:"dependent"`
			StillImage      int `json:"still_image"`
		} `json:"disposition"`
		Tags struct {
			Language    string `json:"language"`
			HandlerName string `json:"handler_name"`
			VendorID    string `json:"vendor_id"`
			Encoder     string `json:"encoder"`
			Timecode    string `json:"timecode"`
		} `json:"tags,omitempty"`
		SampleFmt      string `json:"sample_fmt,omitempty"`
		SampleRate     string `json:"sample_rate,omitempty"`
		Channels       int    `json:"channels,omitempty"`
		ChannelLayout  string `json:"channel_layout,omitempty"`
		BitsPerSample  int    `json:"bits_per_sample,omitempty"`
		InitialPadding int    `json:"initial_padding,omitempty"`
	} `json:"streams"`
}

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

func (cfg apiConfig) getS3AssetURL(assetFilekey string) string {
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, assetFilekey)
}

func (cfg apiConfig) getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to run ffprobe: %v", err)
	}

	// Unmarshal the output to get the width and height
	var ffprobeData ffprobeOutput
	err = json.Unmarshal(out.Bytes(), &ffprobeData)
	if err != nil {
		return "", fmt.Errorf("failed to parse ffprobe output: %v\nOutput: %s", err, out.String())
	}

	if len(ffprobeData.Streams) == 0 {
		return "", fmt.Errorf("no video stream found in ffprobe output")
	}

	// If display aspect ratio is provided by ffprobe, use it. Otherwise, calculate it based on width and height
	width := ffprobeData.Streams[0].Width
	height := ffprobeData.Streams[0].Height
	displayAspectRatio := ffprobeData.Streams[0].DisplayAspectRatio
	log.Printf("ffprobe output - width: %d, height: %d, display aspect ratio: %s", width, height, displayAspectRatio)
	if displayAspectRatio == "" {
		aspectRatio := float64(width) / float64(height)
		if aspectRatio > 1.6 && aspectRatio < 1.8 {
			displayAspectRatio = "16:9"
		} else if aspectRatio > 0.5 && aspectRatio < 0.6 {
			displayAspectRatio = "9:16"
		} else {
			displayAspectRatio = "other"
		}
	}

	// Normalize display aspect ratio to either "16:9", "9:16", or "other"
	if displayAspectRatio != "16:9" && displayAspectRatio != "9:16" {
		displayAspectRatio = "other"
	}
	return displayAspectRatio, nil
}

func (cfg apiConfig) processVideoForFastStart(filePath string) (string, error) {
	faststartFilePath := filePath + "_fs.mp4"
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", faststartFilePath)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to run ffmpeg for fast start optimization: %v\nOutput: %s\nError: %s", err, out.String(), stderr.String())
	}
	return faststartFilePath, nil
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s3Client)
	req, err := presignClient.PresignGetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %v", err)
	}
	return req.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL == nil {
		return video, nil
	}

	// The VideoURL field currently contains the S3 bucket and key in the format "bucket,key" - this is temporary until we implement CloudFront in Chapter 7, Lesson 3
	parts := strings.Split(*video.VideoURL, ",")
	if len(parts) != 2 {
		return video, fmt.Errorf("invalid VideoURL format: %s", *video.VideoURL)
	}
	bucket := parts[0]
	key := parts[1]
	signedURL, err := generatePresignedURL(cfg.s3Client, bucket, key, 15*time.Minute)
	if err != nil {
		return video, fmt.Errorf("failed to generate signed URL for video: %v", err)
	}
	video.VideoURL = &signedURL
	return video, nil
}
