package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cespare/xxhash"
	"github.com/pkg/xattr"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type lutimUploadReply struct {
	Success bool                    `json:"success"`
	Message lutimUploadReplyMessage `json:"msg"`
}

type lutimUploadReplyMessage struct {
	RealShort       string `json:"real_short"`
	Short           string `json:"short"`
	Token           string `json:"token"`
	Thumb           string `json:"thumb"`
	Filename        string `json:"filename"`
	CreatedAt       int64  `json:"created_at"`
	DeleteFirstView bool   `json:"del_at_view"`
	FileExtension   string `json:"ext"`
	LifetimeDays    int    `json:"limit"`
}

type lutimDeleteReply struct {
	Success bool   `json:"success"`
	Msg     string `json:"msg"`
}

type options struct {
	LifetimeDays int
}

type storedFile struct {
	Name          string
	Expires       *time.Time
	DeletionToken string
}

const (
	kExpiresXAttr       = "user.imp.expire"
	kDeletionTokenXAttr = "user.imp.dtoken"

	kDeletionTokenHeader = "X-Deletion-Token"

	kLutimLifetimeArg = "delete-day"

	kGitUrl = "https://github.com/zopieux/improut"
)


var (
	kNameRegexp        = regexp.MustCompile("^[a-f0-9]{16}\\.[a-z]{3,5}$")
	kLutimDeleteRegexp = regexp.MustCompile("/d/([a-f0-9]{16}\\.[a-z]{3,5})/([a-f0-9]{32})$")
)

func storageName(name string) string {
	if !kNameRegexp.MatchString(name) {
		return ""
	}
	return name
}

func storageNameFromRequest(r *http.Request) string {
	return storageName(strings.TrimLeft(r.URL.Path, "/"))
}

func storagePath(storageName string) string {
	return filepath.Join(*storageRoot, storageName)
}

func storeFile(file io.ReadCloser, originalName string, opts *options) (storedFile, error) {
	defer file.Close()
	randBytes := make([]byte, 16)
	if _, err := rand.Read(randBytes); err != nil {
		return storedFile{}, err
	}

	tempPath := storagePath(".tmp-" + hex.EncodeToString(randBytes))
	defer func() { os.Remove(tempPath) }()
	if err := func() error {
		dst, err := os.Create(tempPath)
		defer dst.Close()
		if err != nil {
			return err
		}
		if _, err := io.Copy(dst, file); err != nil {
			return err
		}
		return nil
	}(); err != nil {
		return storedFile{}, err
	}

	ok := false
	digest := xxhash.New()
	file2, err := os.Open(tempPath)
	if err != nil {
		return storedFile{}, err
	}
	defer file2.Close()
	if _, err := io.Copy(digest, file2); err != nil {
		return storedFile{}, err
	}
	file2.Close()
	ext := filepath.Ext(originalName)
	if len(ext) == 0 {
		ext = ".jpg"
	}
	name := hex.EncodeToString(digest.Sum(nil)) + ext
	path := storagePath(name)
	if err := os.Rename(tempPath, path); err != nil {
		return storedFile{}, err
	}

	defer func() {
		if !ok {
			os.Remove(path)
		}
	}()
	if err := xattr.Set(path, kDeletionTokenXAttr, randBytes); err != nil {
		return storedFile{}, err
	}
	var expires *time.Time = nil
	if opts.LifetimeDays > 0 {
		t := time.Now().Add(time.Hour * 24 * time.Duration(opts.LifetimeDays))
		expiresBin, err := t.MarshalBinary()
		if err != nil {
			return storedFile{}, err
		}
		if err := xattr.Set(path, kExpiresXAttr, expiresBin); err != nil {
			return storedFile{}, err
		}
		expires = &t
	} else {
		_ = xattr.Remove(path, kExpiresXAttr)
	}
	ok = true
	log.Printf("Stored file %s (%+v)", path, opts)
	return storedFile{Name: name, Expires: expires, DeletionToken: hex.EncodeToString(randBytes)}, nil
}

func deleteFile(path string, userDeletionToken string) error {
	deletionToken, err := xattr.Get(path, kDeletionTokenXAttr)
	if err != nil || userDeletionToken != hex.EncodeToString(deletionToken) {
		return errors.New("no such file or invalid token")
	}
	if err := os.Remove(path); err == nil {
		log.Printf("Deleted file %s", path)
	}
	return err
}

func parseLifetimeDays(request *http.Request) int {
	lifetimeDays, err := strconv.Atoi(request.FormValue(kLutimLifetimeArg))
	if err != nil || lifetimeDays < 0 {
		lifetimeDays = *defaultLifetimeDays
	}
	if *maxLifetimeDays > 0 && (lifetimeDays == 0 || lifetimeDays > *maxLifetimeDays) {
		lifetimeDays = *maxLifetimeDays
	}
	return lifetimeDays
}

func lutimUpload(writer http.ResponseWriter, request *http.Request) {
	if err := request.ParseMultipartForm(*maxFileSize); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	file, hdr, err := request.FormFile("file")
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	lifetimeDays := parseLifetimeDays(request)
	stored, err := storeFile(file, hdr.Filename, &options{LifetimeDays: lifetimeDays})
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	reply, err := json.Marshal(lutimUploadReply{
		Success: true,
		Message: lutimUploadReplyMessage{
			RealShort:       stored.Name,
			Short:           stored.Name,
			Token:           stored.DeletionToken,
			Thumb:           "",
			Filename:        hdr.Filename,
			CreatedAt:       time.Now().Unix(),
			DeleteFirstView: false,
			FileExtension:   filepath.Ext(stored.Name),
			LifetimeDays:    lifetimeDays,
		},
	})
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	writer.Write(reply)
}

func lutimDelete(writer http.ResponseWriter, request *http.Request) {
	match := kLutimDeleteRegexp.FindStringSubmatch(request.URL.Path)
	if match == nil {
		http.Error(writer, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	name := storageName(match[1])
	if name == "" {
		http.Error(writer, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	deletionToken := match[2]
	deleteErr := deleteFile(storagePath(name), deletionToken)
	reply, err := json.Marshal(lutimDeleteReply{
		Success: deleteErr == nil,
		Msg: func() string {
			if deleteErr == nil {
				return "file deleted"
			} else {
				return deleteErr.Error()
			}
		}(),
	})
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	writer.WriteHeader(map[bool]int{
		true:  http.StatusOK,
		false: http.StatusBadRequest,
	}[deleteErr == nil])
	writer.Write(reply)
}

func restUpload(writer http.ResponseWriter, request *http.Request) {
	if err := request.ParseMultipartForm(*maxFileSize); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	file, hdr, err := request.FormFile("file")
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	lifetimeDays := parseLifetimeDays(request)
	stored, err := storeFile(file, hdr.Filename, &options{LifetimeDays: lifetimeDays})
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	writer.Header().Set(kDeletionTokenHeader, stored.DeletionToken)
	if stored.Expires != nil {
		writer.Header().Set("Expires", stored.Expires.Format(http.TimeFormat))
	}
	http.Redirect(writer, request, "/"+stored.Name, 302)
}

func restDelete(writer http.ResponseWriter, request *http.Request) {
	name := storageNameFromRequest(request)
	if name == "" {
		http.NotFound(writer, request)
		return
	}
	if err := deleteFile(storagePath(name), request.Header.Get(kDeletionTokenHeader)); err != nil {
		http.NotFound(writer, request)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func dispatch(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if request.URL.Path == "/" {
			writer.Write([]byte(fmt.Sprintf(`
improut ⋅ dead simple image hosting

Upload:
  $ curl -v -F file=@image.png [ -F delete-day=<lifetime in days> ] /
	Returns a 302 redirect to the image, with %s header for deletion.

  or (Lutim compatibility):
  $ curl -v -F file=@image.png -F format=json [ -F delete-day=<lifetime in days> ] /
	Returns a JSON reply which includes the deletion token.

Delete existing image:
	$ curl -v -X DELETE -H '%s: <token>' /<image path>

  or (Lutim compatibility):
	$ curl -v /d/<image path>/<token>

This is open-source software under MIT license:
%s
`, kDeletionTokenHeader, kDeletionTokenHeader, kGitUrl)))
			return
		}
		if kLutimDeleteRegexp.MatchString(request.URL.Path) {
			lutimDelete(writer, request)
			return
		}
		name := storageNameFromRequest(request)
		if name == "" {
			http.NotFound(writer, request)
			return
		}
		if *xAccel == "" {
			http.ServeFile(writer, request, storagePath(name))
		} else {
			redirect := *xAccel + "/" + name
			writer.Header().Set("X-Accel-Redirect", redirect)
			writer.WriteHeader(204)
		}
	case http.MethodPost:
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}
		if err := request.ParseMultipartForm(*maxFileSize); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if request.FormValue("format") == "json" {
			lutimUpload(writer, request)
		} else {
			restUpload(writer, request)
		}
	case http.MethodDelete:
		restDelete(writer, request)
	default:
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

func lutimInfo(writer http.ResponseWriter, request *http.Request) {
	reply, err := json.Marshal(struct {
		AlwaysEncrypt    bool   `json:"always_encrypt"`
		BroadcastMessage string `json:"broadcast_message"`
		Contact          string `json:"contact"`
		DefaultDelay     int    `json:"default_delay"`
		ImageMagick      bool   `json:"image_magick"`
		MaxDelay         int    `json:"max_delay"`
		MaxFileSize      int64  `json:"max_file_size"`
	}{
		AlwaysEncrypt:    false,
		BroadcastMessage: *lutimMotd,
		Contact:          kGitUrl,
		DefaultDelay:     *defaultLifetimeDays,
		ImageMagick:      false,
		MaxDelay:         *maxLifetimeDays,
		MaxFileSize:      *maxFileSize,
	})
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	writer.Write(reply)
}
