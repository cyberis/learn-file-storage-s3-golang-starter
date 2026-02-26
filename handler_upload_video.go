package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/cyberis/learn-file-storage-s3-golang/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {

	// Set up a maximum video size limit of 1 GB for parsing the multipart form data to prevent abuse
	const maxVideoSize = 1 << 30 // 1 GB
	r.Body = http.MaxBytesReader(w, r.Body, maxVideoSize)

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	// Make sure the video exists and belongs to the user
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Couldn't get video", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusForbidden, "You don't have permission to upload a video for this video ID", nil)
		return
	}

	fmt.Println("uploading video", videoID, "by user", userID)

	// Parse the multipart form in the request
	err = r.ParseMultipartForm(maxVideoSize)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Video too large or couldn't parse multipart form", err)
		return
	}
	// Get the file from the form data
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't get video file from form", err)
		return
	}
	defer file.Close()

	// Validate the media type of the uploaded file
	mediaType := header.Header.Get("Content-Type")
	mediaType, _, err = mime.ParseMediaType(mediaType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse media type", err)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Unsupported media type. Only MP4 videos are allowed.", nil)
		return
	}

	// Set the filename based on the media type
	filename, err := getAssetFilename(mediaType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't determine file extension from media type", err)
		return
	}

	// Save temp file to disk before uploading to S3
	tempFile, err := os.CreateTemp("", "tubely-upload-*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create temp file", err)
		return
	}
	defer tempFile.Close()
	defer os.Remove(tempFile.Name()) // Clean up temp file after we're done

	// Copy the uploaded video to the temp file
	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't save video to temp file", err)
		return
	}

	// Get the aspect ratio of the video using ffprobe and update the S3 file key based on the aspect ratio
	// to organize videos in S3 by aspect ratio (e.g. landscape, portrait, other)
	aspectRatio, err := cfg.getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get video aspect ratio", err)
		return
	}

	// Set the file key for S3 based on the filename and the aspect ratio
	var filekey string
	switch aspectRatio {
	case "16:9":
		filekey = fmt.Sprintf("landscape/%s", filename)
	case "9:16":
		filekey = fmt.Sprintf("portrait/%s", filename)
	default:
		filekey = fmt.Sprintf("other/%s", filename)
	}

	// Optimize the video for fast start by moving the moov atom to the beginning of the file using ffmpeg
	tempFile.Close()
	fastStartVideoPath, err := cfg.processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't optimize video for fast start", err)
		return
	}
	fastStartVideoFile, err := os.Open(fastStartVideoPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't open optimized video file", err)
		return
	}
	defer fastStartVideoFile.Close()
	defer os.Remove(fastStartVideoPath) // Clean up optimized video file after we're done

	// Upload the video to S3
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(filekey),
		Body:        fastStartVideoFile,
		ContentType: aws.String(mediaType),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't upload video to S3", err)
		return
	}

	// Create the S3 URL for the video
	videoURL := cfg.getS3AssetURL(filekey)

	// This is termporary code for Chapter 6, Lesson 6: Signed URLs -- Will be removed in Chapter 7, Lesson 3 Use CloudFront
	videoURL = fmt.Sprintf("%s,%s", cfg.s3Bucket, filekey)

	// Update the video record with the video URL
	video.VideoURL = &videoURL
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video record with S3 URL", err)
		return
	}

	// Get the signed URL for the video to return in the response - Temporrary code for Chapter 6, Lesson 6: Signed URLs -- Will be removed in Chapter 7, Lesson 3 Use CloudFront
	video, err = cfg.dbVideoToSignedVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't generate signed URL for video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}
