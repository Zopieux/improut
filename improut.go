package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
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
	listenAddr                = flag.String("listen", ":8000", "listen address (host:port)")
	maxFileSize               = flag.Int64("max-size", 10<<20, "max file size (bytes)")
	maxLifetimeDays           = flag.Int("max-lifetime", 0, "0 for infinite retention")
	lutimeDefaultLitefimeDays = flag.Int("default-lifetime", 7, "Lutim: default lifetime (days, 0 for infinite)")
	lutimMotd                 = flag.String("motd", "", "Lutim: message of the day")
	storageRoot               = flag.String("root", "/var/lib/improut", "root storage directory")
)

var (
	kNameRegexp        = regexp.MustCompile("^[a-f0-9]{16}\\.[a-z]{3,5}$")
	kLutimDeleteRegexp = regexp.MustCompile("/d/([a-f0-9]{16}\\.[a-z]{3,5})/([a-f0-9]{32})$")
)

func storagePathFromRequest(r *http.Request) string {
	return storagePath(strings.TrimLeft(r.URL.Path, "/"))
}

func storagePath(name string) string {
	if !kNameRegexp.MatchString(name) {
		return ""
	}
	return filepath.Join(*storageRoot, name)
}

func storeFile(file io.ReadCloser, originalName string, opts *options) (storedFile, error) {
	defer file.Close()
	randBytes := make([]byte, 16)
	if _, err := rand.Read(randBytes); err != nil {
		return storedFile{}, err
	}

	tempPath := filepath.Join(*storageRoot, ".tmp-"+hex.EncodeToString(randBytes))
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
	if opts.LifetimeDays > 0 {
		expires := time.Now().Add(time.Hour * 24 * time.Duration(opts.LifetimeDays))
		expiresBin, err := expires.MarshalBinary()
		if err != nil {
			return storedFile{}, err
		}
		if err := xattr.Set(path, kExpiresXAttr, expiresBin); err != nil {
			return storedFile{}, err
		}
	}
	ok = true
	log.Printf("Stored file %s (%+v)", path, opts)
	return storedFile{Name: name, DeletionToken: hex.EncodeToString(randBytes)}, nil
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

func lutimUpload(writer http.ResponseWriter, request *http.Request) {
	if err := request.ParseMultipartForm(*maxFileSize); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	lifetimeDays, err := strconv.Atoi(request.FormValue(kLutimLifetimeArg))
	if err != nil || lifetimeDays < 1 {
		lifetimeDays = 0
	}
	if *maxLifetimeDays > 0 && lifetimeDays > *maxLifetimeDays {
		lifetimeDays = *maxLifetimeDays
	}
	file, hdr, err := request.FormFile("file")
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
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
	name := match[1]
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
	stored, err := storeFile(file, hdr.Filename, &options{LifetimeDays: 0})
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	writer.Header().Set(kDeletionTokenHeader, stored.DeletionToken)
	http.Redirect(writer, request, "/"+stored.Name, 302)
}

func restDelete(writer http.ResponseWriter, request *http.Request) {
	path := storagePathFromRequest(request)
	if path == "" {
		http.NotFound(writer, request)
		return
	}
	if err := deleteFile(path, request.Header.Get(kDeletionTokenHeader)); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func detectLutim(request *http.Request) bool {
	return request.FormValue("format") != "" || request.FormValue(kLutimLifetimeArg) != ""
}

func dispatch(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if kLutimDeleteRegexp.MatchString(request.URL.Path) {
			lutimDelete(writer, request)
			return
		}
		path := storagePathFromRequest(request)
		if path == "" {
			writer.Write([]byte(fmt.Sprintf(`
improut ⋅ dead simple image hosting

Upload:
  POST / (multipart/format-data)
             file=<image data>
    [ delete-days=<lifetime in integer days> ]

  Returns either:
    * JSON if Lutim is detected, which includes the deletion token.
    * Otherwise a 302 redirect to the image, with header %s.

Delete existing image:
  DELETE /<image path> with header %s from above.
  or 
  GET /d/<image path>/<deletion token>


This is open-source software under MIT license:
%s
`, kDeletionTokenHeader, kDeletionTokenHeader, kGitUrl)))
			return
		}
		http.ServeFile(writer, request, path)
	case http.MethodPost:
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}
		if err := request.ParseMultipartForm(*maxFileSize); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if detectLutim(request) {
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
		Contact:          "http://github.com/zopieux/improut/",
		DefaultDelay:     *lutimeDefaultLitefimeDays,
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

func checkExpired() {
	for {
		now := time.Now()
		var t time.Time
		filepath.Walk(*storageRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			binary, err := xattr.Get(path, kExpiresXAttr)
			if err != nil {
				return nil
			}
			if err := t.UnmarshalBinary(binary); err != nil {
				return nil
			}
			if t.Before(now) {
				log.Printf("Removing file %s (expired %v, %v)", filepath.Base(path), t, t.Sub(now))
				os.Remove(path)
			}
			return nil
		})
		time.Sleep(24 * 60 * 1000 * time.Millisecond)
	}
}

func main() {
	flag.Parse()

	if err := os.MkdirAll(*storageRoot, 0750); err != nil {
		log.Fatal(err)
	}

	go checkExpired()

	http.HandleFunc("/infos", lutimInfo)
	http.HandleFunc("/", dispatch)
	http.ListenAndServe(*listenAddr, nil)
}
