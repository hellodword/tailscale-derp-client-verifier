package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

const (
	defaultAddr = "localhost:3000"
)

type Fetcher func() ([]key.NodePublic, error)
type Searcher func(n key.NodePublic) bool

func main() {
	addr := flag.String("addr", defaultAddr, "")
	nodesFile := flag.String("path", "", "/path/to/nodes.json")
	flag.Parse()

	var interval time.Duration
	var fetcher Fetcher
	var searcher Searcher

	if *nodesFile == "" {
		interval = time.Minute * 10
		fetcher = setupS3Fetcher()
		_, err := fetcher()
		if err != nil {
			panic(err)
		}
	} else {
		interval = time.Minute
		fetcher = func() ([]key.NodePublic, error) {
			var nodes []key.NodePublic
			err := readJSONFile(*nodesFile, &nodes)
			return nodes, err
		}
	}

	var lock sync.RWMutex
	var nodes []key.NodePublic
	var lastUpdate uint32

	searcher = func(n key.NodePublic) bool {
		now := uint32(time.Now().Unix())

		if now > atomic.LoadUint32(&lastUpdate)+uint32(interval.Seconds()) {
			atomic.StoreUint32(&lastUpdate, now)
			log.Println("fetcher", "fetching")
			_nodes, err := fetcher()
			if err != nil {
				log.Println("fetcher", err)
			} else {
				lock.Lock()
				nodes = _nodes
				lock.Unlock()
				log.Println("fetcher", "updated")
			}
		}

		lock.RLock()
		defer lock.RUnlock()
		for i := range nodes {
			if n.Compare(nodes[i]) == 0 {
				return true
			}
		}
		return false
	}

	http.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var buf = bytes.NewBuffer(nil)
		var req tailcfg.DERPAdmitClientRequest
		err := json.NewDecoder(io.TeeReader(io.LimitReader(r.Body, 1<<13), buf)).Decode(&req)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var resp tailcfg.DERPAdmitClientResponse

		resp.Allow = searcher(req.NodePublic)

		if resp.Allow {
			log.Println("allowed", req.NodePublic, req.Source)
		}

		b, err := json.Marshal(resp)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(b)
	}))

	log.Println("serving", *addr)
	err := http.ListenAndServe(*addr, nil)
	if err != nil {
		os.Exit(1)
	}
}

func setupS3Fetcher() Fetcher {
	s3AccessKey := os.Getenv("S3_ACCESS_KEY_ID")
	s3SecretKey := os.Getenv("S3_SECRET_ACCESS_KEY")
	s3Endpoint := os.Getenv("S3_ENDPOINT")
	s3Region := os.Getenv("S3_REGION")
	s3Bucket := os.Getenv("S3_BUCKET")
	s3File := os.Getenv("S3_FILE")
	s3ForcePathStyle := strings.ToLower(os.Getenv("S3_FORCE_PATH_STYLE")) == "true"
	s3Object := &s3.GetObjectInput{
		Bucket: aws.String(s3Bucket),
		Key:    aws.String(s3File),
	}

	cfg := &aws.Config{
		Endpoint:         aws.String(s3Endpoint),
		Region:           aws.String(s3Region),
		S3ForcePathStyle: aws.Bool(s3ForcePathStyle),
		Credentials:      credentials.NewStaticCredentials(s3AccessKey, s3SecretKey, ""),
	}
	sess := session.Must(session.NewSession(cfg))

	s3Instance := s3.New(sess)

	return func() ([]key.NodePublic, error) {
		var nodes []key.NodePublic
		err := readJSONS3(s3Instance, s3Object, &nodes)
		return nodes, err
	}
}

func readJSONS3(instance *s3.S3, object *s3.GetObjectInput, v interface{}) error {
	r, err := instance.GetObject(object)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func readJSONFile(path string, v interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}
