package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"

	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func main() {

	addr := flag.String("addr", "localhost:3000", "")
	verifyFile := flag.String("path", "verifier.json", "")
	flag.Parse()

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

		f, err := os.Open(*verifyFile)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Println("open", err)
			return
		}

		var nodes []key.NodePublic
		err = json.NewDecoder(f).Decode(&nodes)
		f.Close()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Println("decode", err)
			return
		}

		var resp tailcfg.DERPAdmitClientResponse

		for i := range nodes {
			if req.NodePublic.Compare(nodes[i]) == 0 {
				resp.Allow = true
				log.Println("allowed", req.NodePublic.String(), req.Source)
				break
			}
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
