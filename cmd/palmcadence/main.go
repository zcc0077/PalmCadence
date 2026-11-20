package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zcc0077/PalmCadence/internal/catalog"
)

func main() {
	port := flag.Int("port", 8787, "local HTTP port")
	web := flag.String("web", "", "serve the web UI from this directory for browser development")
	ca := flag.String("ca", "", "CA bundle on legacy webOS")
	cacheDir := flag.String("cache", "", "directory for the local audio cache")
	storefront := flag.String("storefront", "US", "two-letter Apple storefront")
	flag.Parse()

	if *port < 1024 || *port > 65535 || !regexp.MustCompile(`^[A-Za-z]{2}$`).MatchString(*storefront) {
		log.Fatal("invalid port or storefront")
	}
	clientTransport := http.DefaultTransport.(*http.Transport).Clone()
	clientTransport.Proxy = nil
	clientTransport.DialContext = (&net.Dialer{Timeout: 6 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	clientTransport.ResponseHeaderTimeout = 8 * time.Second
	clientTransport.MaxIdleConns = 10
	clientTransport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if *ca != "" {
		pem, err := os.ReadFile(*ca)
		if err != nil {
			log.Fatal(err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			log.Fatal("no certificates in CA bundle")
		}
		clientTransport.TLSClientConfig.RootCAs = roots
	}
	catalogClient := &http.Client{
		Timeout: 12 * time.Second, Transport: clientTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			first := via[0].URL
			if len(via) >= 3 || req.URL.Host != first.Host || req.URL.Scheme != first.Scheme {
				return errors.New("unexpected catalog redirect")
			}
			return nil
		},
	}
	portString := strconv.Itoa(*port)
	address := "127.0.0.1:" + portString
	app := appServer{
		catalog: catalog.Catalog{Client: catalogClient, Storefront: strings.ToUpper(*storefront)},
		media: &http.Client{
			Timeout: 15 * time.Minute, Transport: clientTransport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		entries: make(map[string]stream),
		host:    address, localhost: "localhost:" + portString,
	}
	if *web != "" {
		app.web = http.FileServer(http.Dir(*web))
	}
	if *cacheDir != "" {
		cache, err := openAudioCache(*cacheDir)
		if err != nil {
			log.Printf("audio cache unavailable: %v", err)
		} else {
			app.cache = cache
		}
	}
	server := &http.Server{Addr: address, Handler: &app, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	log.Printf("PalmCadence listening at http://%s", address)
	log.Fatal(server.ListenAndServe())
}

func logFailure(source string, err error) {
	var requestError *url.Error
	if errors.As(err, &requestError) {
		err = requestError.Err
	}
	log.Printf("%s: %v", source, err)
}
