package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

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

	// Upload the video to S3
	tempFile.Seek(0, io.SeekStart) // Reset file pointer to the beginning before uploading

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &filename,
		Body:        tempFile,
		ContentType: &mediaType,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't upload video to S3", err)
		return
	}

	// Create the S3 URL for the video
	videoURL := cfg.getS3AssetURL(filename)

	// Update the video record with the video URL
	video.VideoURL = &videoURL
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video record with S3 URL", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}
