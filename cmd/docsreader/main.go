// Command docsreader runs the local book reader.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/frank5ive/docsreader/internal/adapters/format/epub"
	"github.com/frank5ive/docsreader/internal/adapters/format/pdf"
	"github.com/frank5ive/docsreader/internal/adapters/platform"
	"github.com/frank5ive/docsreader/internal/adapters/storage/sqlite"
	"github.com/frank5ive/docsreader/internal/adapters/web"
	"github.com/frank5ive/docsreader/internal/app"
	"github.com/frank5ive/docsreader/internal/domain"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dataDir := flag.String("data", "", "data directory (default: per-user config dir)")
	port := flag.Int("port", 0, "port to listen on (default: random)")
	noOpen := flag.Bool("no-browser", false, "do not open the browser")
	flag.Parse()

	dir := *dataDir
	if dir == "" {
		d, err := platform.DataDir()
		if err != nil {
			return err
		}
		dir = d
	} else if err := os.MkdirAll(filepath.Join(dir, "books"), 0o700); err != nil {
		return err
	}

	store, err := sqlite.Open(filepath.Join(dir, "docsreader.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	readers := map[domain.Format]app.FormatReader{
		domain.FormatEPUB: epub.Reader{},
		domain.FormatPDF:  pdf.Reader{},
	}
	lib := &app.Library{Books: store, Files: platform.FileStore{Dir: filepath.Join(dir, "books")}, Readers: readers, Now: time.Now}
	rdg := &app.Reading{Books: store, Progress: store.Progress(), Sessions: store, Readers: readers, Now: time.Now}

	// Import any files passed on the command line.
	for _, p := range flag.Args() {
		if _, err := lib.Import(context.Background(), p); err != nil {
			log.Printf("import %s: %v", p, err)
		}
	}

	srv := web.New(lib, rdg)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", ln.Addr().(*net.TCPAddr).Port, srv.Token())
	fmt.Println("docsreader running at", url)
	if !*noOpen {
		if err := platform.OpenBrowser(url); err != nil {
			log.Printf("could not open browser: %v", err)
		}
	}

	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
