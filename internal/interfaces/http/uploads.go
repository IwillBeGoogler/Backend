package http

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/images"
)

const maxUpload = 5 << 20 // 5 MB

// Allowed image types, detected from content (not the client's header).
var imageExt = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

type uploaded struct {
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"` // ready a moment after the upload
	// Variants are WebP copies by width (320, 640, 1280; never upscaled),
	// ready with the thumbnail. SrcSet is them as an <img srcset> value.
	Variants map[string]string `json:"variants"`
	SrcSet   string            `json:"srcset"`
}

func newUploaded(key string) uploaded {
	u := uploaded{URL: "/api/uploads/" + key, ThumbnailURL: "/api/uploads/" + application.ThumbKey(key), Variants: map[string]string{}}
	var set []string
	for _, w := range application.VariantWidths {
		url := "/api/uploads/" + application.VariantKey(key, w)
		u.Variants[strconv.Itoa(w)] = url
		set = append(set, url+" "+strconv.Itoa(w)+"w")
	}
	u.SrcSet = strings.Join(set, ", ")
	return u
}

// upload stores an image in MinIO and queues a JPEG thumbnail and WebP
// variants (320, 640, 1280 wide) for responsive images.
//
// Multipart field "file". JPEG, PNG, GIF or WebP up to 5 MB, detected from
// content. Every image is stored as WebP, so url ends in .webp: a JPEG lossy
// and turned upright by its EXIF orientation, a PNG and a still GIF lossless
// (transparency kept), an animated GIF as an animated WebP, a WebP
// compressed again when that makes it smaller. EXIF, XMP and text metadata
// are dropped. Use url as cover_url or avatar_url, and srcset in <img srcset>.
// Rate limit: 10 per minute. An account may store UPLOAD_QUOTA_MB in total
// (409 once it is full).
// Auth: upload.create.
//
// spector:tags uploads
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	u, ok := h.requireUser(w, r)
	if !ok || !h.allow(w, r, "upload", 10, time.Minute) {
		return
	}
	if err := h.Blog.Authz.Can(r.Context(), "upload", "create", 0, 0); err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<10)
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "file required (max 5 MB)")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "file required (max 5 MB)")
		return
	}
	if len(data) > maxUpload {
		writeError(w, r, http.StatusBadRequest, "file too large (max 5 MB)")
		return
	}
	ctype := http.DetectContentType(data)
	ext, ok := imageExt[ctype]
	if !ok {
		writeError(w, r, http.StatusBadRequest, "only jpeg, png, gif, webp images")
		return
	}
	if err := images.Validate(data); err != nil {
		writeError(w, r, http.StatusBadRequest, "not a valid image")
		return
	}
	// Every image is stored as WebP: smaller, and without EXIF or other metadata.
	if out, err := images.ToWebP(data); err != nil {
		// A picture that decodes but cannot be converted (an animation too big to hold in memory, an encoder
		// failure): keep the upload in its own format, stripped of metadata below.
		zap.L().Warn("upload to webp", zap.Any("ctx", r.Context()), zap.String("type", ctype), zap.Error(err))
	} else {
		data, ctype, ext = out, "image/webp", imageExt["image/webp"]
	}
	data = images.StripMetadata(data)
	// Two uploads racing past the check can exceed the quota by one file; the next one is refused.
	if h.Uploads != nil && h.UploadQuotaBytes > 0 {
		used, err := h.Uploads.Usage(r.Context(), u.ID)
		if err != nil {
			respond(w, r, 0, nil, err, "")
			return
		}
		if used+int64(len(data)) > h.UploadQuotaBytes {
			writeError(w, r, http.StatusConflict, "upload storage is full ("+strconv.FormatInt(h.UploadQuotaBytes>>20, 10)+" MB per account)")
			return
		}
	}
	name := make([]byte, 16)
	if _, err := rand.Read(name); err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	key := hex.EncodeToString(name) + ext
	if err := h.Storage.Put(r.Context(), key, bytes.NewReader(data), int64(len(data)), ctype); err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	if h.Uploads != nil {
		if err := h.Uploads.Record(r.Context(), key, u.ID, int64(len(data))); err != nil {
			// The image is stored and usable; it only goes uncounted.
			zap.L().Warn("upload ledger", zap.Any("ctx", r.Context()), zap.String("key", key), zap.Error(err))
		}
	}
	if err := h.Jobs.Thumbnail(r.Context(), key); err != nil {
		// The original is stored; only derived sizes are missing. Clients fall
		// back to the original, so do not fail (and orphan) the upload.
		zap.L().Warn("thumbnail enqueue", zap.Any("ctx", r.Context()), zap.String("key", key), zap.Error(err))
	}
	writeJSON(w, http.StatusCreated, newUploaded(key))
}

// download serves an uploaded image or its thumbnail from MinIO.
//
// Auth: none.
//
// spector:tags uploads
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" || strings.Contains(key, "..") {
		writeError(w, r, http.StatusNotFound, "not found")
		return
	}
	obj, err := h.Storage.Get(r.Context(), key)
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respond(w, r, 0, nil, err, "")
		return
	}
	defer obj.Body.Close()
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if _, err := io.Copy(w, obj.Body); err != nil {
		zap.L().Debug("download aborted", zap.String("key", key), zap.Error(err))
	}
}
