package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Invalid ID", err)
		return
	}
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Could not find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Could not validate JWT", err)
		return
	}

	v, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Could not retrieve video", err)
		return
	}
	if v.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You can not access this users video", err)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadGateway, "Could. not Parse file", err)
		return
	}
	defer file.Close()
	contentType := header.Header.Get("Content-Type")

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Media type not found", err)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "invalid media type", err)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not create file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not copy to file", err)
		return
	}

	fastStartPath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "can not write fast start file", err)
		return
	}

	s3FilePath, err := os.Open(fastStartPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "s3 fast start path could not be opened", err)
	}

	aspectRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "aspect ratio could not be found", err)
		return
	}

	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusBadRequest, "Could't reset file pointer", err)
		return
	}

	key := make([]byte, 32)
	_, err = rand.Read(key)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "could not generate key", err)
		return
	}
	keyStr := hex.EncodeToString(key)
	fileKey := fmt.Sprintf("%s/%s.mp4", aspectRatio, keyStr)

	if _, err := cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileKey,
		Body:        s3FilePath,
		ContentType: &mediaType,
	}); err != nil {
		respondWithError(w, http.StatusBadRequest, "File could not be uploaded to the cloud", err)
		return
	}

	disDomainKey := fmt.Sprintf("%s%s", cfg.s3CfDistribution, fileKey)

	v.VideoURL = &disDomainKey
	v.UpdatedAt = time.Now()

	err = cfg.db.UpdateVideo(v)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not update video records", err)
		return
	}

	respondWithJSON(w, http.StatusOK, v)
}

type probeResponse struct {
	Streams []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"streams"`
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return "", err
	}

	var results probeResponse
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		return "", err
	}

	width := results.Streams[0].Width
	height := results.Streams[0].Height

	ratio := float64(width) / float64(height)

	if ratio > 1.7 {
		return "landscape", nil
	} else if ratio < 0.6 {
		return "portrait", nil
	}

	return "other", nil
}

func processVideoForFastStart(filePath string) (string, error) {
	newPath := filePath + ".processing"

	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", newPath)
	if err := cmd.Run(); err != nil {
		return "", err
	}

	return newPath, nil
}
