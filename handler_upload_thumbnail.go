package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20

	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusRequestEntityTooLarge, "file is too large", err)
		return
	}

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form", err)
		return
	}
	contentType := header.Header.Get("Content-Type")

	defer file.Close()

	medType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Media type not found", err)
		return
	}
	if medType != "image/jpeg" && medType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Invalid media type", nil)
		return
	}

	v, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}

	if v.ID == uuid.Nil {
		respondWithError(w, http.StatusNotFound, "Video not found", nil)
		return
	}

	if userID != v.UserID {
		respondWithError(w, http.StatusUnauthorized, "You don't own this video", nil)
		return
	}

	fileExt := strings.TrimPrefix(medType, "image/")
	assetPath := getAssetPath(fileExt)
	assetDiskPath := cfg.getAssetDiskPath(assetPath)

	dst, err := os.Create(assetDiskPath)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "could not create file", err)
	}

	defer dst.Close()

	_, err = io.Copy(dst, file)
	thumbnailURL := fmt.Sprintf("http://localhost:%s/%s", cfg.port, assetPath)
	v.ThumbnailURL = &thumbnailURL

	err = cfg.db.UpdateVideo(v)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "could not update database", err)
		return
	}

	respondWithJSON(w, http.StatusOK, v)
}
