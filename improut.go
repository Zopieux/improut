package main

import (
	"flag"
	"github.com/pkg/xattr"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var (
	listenAddr               = flag.String("listen", ":8000", "listen address (host:port)")
	maxFileSize              = flag.Int64("max-size", 10<<20, "max file size (bytes)")
	maxLifetimeDays          = flag.Int("max-lifetime", 0, "maximum lifetime (days, 0 for no limit)")
	defaultLifetimeDays      = flag.Int("default-lifetime", 7, "default lifetime (days, 0 for infinite)")
	lutimMotd                = flag.String("motd", "", "Lutim: message of the day")
	xAccel                   = flag.String("xaccel", "", "if non-empty, use X-Accel-Redirect with this root path, instead of serving files ourselves")
	expireCheckIntervalHours = flag.Int("expire-interval", 1, "delay between two expiration checks (hours)")
	storageRoot              = flag.String("root", "/var/lib/improut", "root storage directory")
)

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
		time.Sleep(time.Duration(*expireCheckIntervalHours) * 60 * 1000 * time.Millisecond)
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
