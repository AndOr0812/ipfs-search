package crawler

import (
	"encoding/json"
	"fmt"
	"github.com/ipfs-search/ipfs-search/indexer"
	"github.com/ipfs-search/ipfs-search/queue"
	"github.com/ipfs/go-ipfs-api"
	"log"
	"net"
	"net/http"
	"net/url"
	// "path"
	"strings"
	"syscall"
	"time"
)

const (
	// Reconnect time in seconds
	reconnectWait = 2
	tikaTimeout   = 300

	// Don't attempt to get metadata for files over this size
	metadataMaxSize = 50 * 1024 * 1024

	// Size for partial items - this is the default chunker block size
	// TODO: replace by a sane method of skipping partials
	partialSize = 262144

	// ipfs-tika endpoint URL
	ipfsTikaURL = "http://localhost:8081"
)

// Args describe a resource to be crawled
type Args struct {
	Hash       string
	Name       string
	Size       uint64
	ParentHash string
	ParentName string // This is legacy, should be removed
}

// Crawler consumes file and hash queues and indexes them
type Crawler struct {
	sh *shell.Shell
	id *indexer.Indexer
	fq *queue.TaskQueue
	hq *queue.TaskQueue
}

// NewCrawler initialises a new Crawler
func NewCrawler(sh *shell.Shell, id *indexer.Indexer, fq *queue.TaskQueue, hq *queue.TaskQueue) *Crawler {
	return &Crawler{
		sh: sh,
		id: id,
		fq: fq,
		hq: hq,
	}
}

func hashURL(hash string) string {
	return fmt.Sprintf("/ipfs/%s", hash)
}

// Update references with name, parentHash and parentName. Returns true when updated
func updateReferences(references []indexer.Reference, name string, parentHash string) ([]indexer.Reference, bool) {
	if references == nil {
		// Initialize empty references when none have been found
		references = []indexer.Reference{}
	}

	if parentHash == "" {
		// No parent hash for item, not adding reference
		return references, false
	}

	for _, reference := range references {
		if reference.ParentHash == parentHash {
			// Reference exists, not updating
			return references, false
		}
	}

	references = append(references, indexer.Reference{
		Name:       name,
		ParentHash: parentHash,
	})

	return references, true
}

// Handle IPFS errors graceously, returns try again bool and original error
func (c Crawler) handleError(err error, hash string) (bool, error) {
	if _, ok := err.(*shell.Error); ok && strings.Contains(err.Error(), "proto") {
		// We're not recovering from protocol errors, so panic

		// Attempt to index panic to prevent re-indexing
		metadata := map[string]interface{}{
			"error": err.Error(),
		}

		c.id.IndexItem("invalid", hash, metadata)

		panic(err)
	}

	if uerr, ok := err.(*url.Error); ok {
		// URL errors

		log.Printf("URL error %v", uerr)

		if uerr.Timeout() {
			// Fail on timeouts
			return false, err
		}

		if uerr.Temporary() {
			// Retry on other temp errors
			return true, nil
		}

		// Somehow, the errors below are not temp errors !?
		switch t := uerr.Err.(type) {
		case *net.OpError:
			if t.Op == "dial" {
				log.Printf("Unknown host %v", t)
				return true, nil

			} else if t.Op == "read" {
				log.Printf("Connection refused %v", t)
				return true, nil
			}

		case syscall.Errno:
			if t == syscall.ECONNREFUSED {
				log.Printf("Connection refused %v", t)
				return true, nil
			}
		}
	}

	return false, err
}

func (c Crawler) indexReferences(hash string, name string, parentHash string) ([]indexer.Reference, bool, error) {
	var alreadyIndexed bool

	references, itemType, err := c.id.GetReferences(hash)
	if err != nil {
		return nil, false, err
	}

	// TODO: Handle this more explicitly, use and detect NotFound
	if references == nil {
		alreadyIndexed = false
	} else {
		alreadyIndexed = true
	}

	references, referencesUpdated := updateReferences(references, name, parentHash)

	if alreadyIndexed {
		if referencesUpdated {
			log.Printf("Found %s, reference added: '%s' from %s", hash, name, parentHash)

			properties := map[string]interface{}{
				"references": references,
			}

			err := c.id.IndexItem(itemType, hash, properties)
			if err != nil {
				return nil, false, err
			}
		} else {
			log.Printf("Found %s, references not updated.", hash)
		}
	} else if referencesUpdated {
		log.Printf("Adding %s, reference '%s' from %s", hash, name, parentHash)
	}

	return references, alreadyIndexed, nil
}

// CrawlHash crawls a particular hash (file or directory)
func (c Crawler) CrawlHash(hash string, name string, parentHash string, parentName string) error {
	references, alreadyIndexed, err := c.indexReferences(hash, name, parentHash)

	if err != nil {
		return err
	}

	if alreadyIndexed {
		return nil
	}

	log.Printf("Crawling hash '%s' (%s)", hash, name)

	url := hashURL(hash)

	var list *shell.UnixLsObject

	tryAgain := true
	for tryAgain {
		list, err = c.sh.FileList(url)

		tryAgain, err = c.handleError(err, hash)

		if tryAgain {
			log.Printf("Retrying in %d seconds", reconnectWait)
			time.Sleep(reconnectWait * time.Duration(time.Second))
		}
	}

	if err != nil {
		return err
	}

	switch list.Type {
	case "File":
		// Add to file crawl queue
		args := Args{
			Hash:       hash,
			Name:       name,
			Size:       list.Size,
			ParentHash: parentHash,
		}

		err = c.fq.AddTask(args)
		if err != nil {
			// failed to send the task
			return err
		}
	case "Directory":
		// Queue indexing of linked items
		for _, link := range list.Links {
			args := Args{
				Hash:       link.Hash,
				Name:       link.Name,
				Size:       link.Size,
				ParentHash: hash,
			}

			switch link.Type {
			case "File":
				// Add file to crawl queue
				err = c.fq.AddTask(args)
				if err != nil {
					// failed to send the task
					return err
				}

			case "Directory":
				// Add directory to crawl queue
				c.hq.AddTask(args)
				if err != nil {
					// failed to send the task
					return err
				}
			default:
				log.Printf("Type '%s' skipped for '%s'", list.Type, hash)
			}
		}

		// Index name and size for directory and directory items
		properties := map[string]interface{}{
			"links":      list.Links,
			"size":       list.Size,
			"references": references,
		}

		// Skip partial content
		if list.Size == partialSize && parentHash == "" {
			// Assertion error.
			// REMOVE ME!
			log.Printf("Skipping unreferenced partial content for directory %s", hash)
			return nil
		}

		err := c.id.IndexItem("directory", hash, properties)
		if err != nil {
			return err
		}

	default:
		log.Printf("Type '%s' skipped for '%s'", list.Type, hash)
	}

	log.Printf("Finished hash %s", hash)

	return nil
}

func getMetadata(path string, metadata *map[string]interface{}) error {
	client := http.Client{
		Timeout: tikaTimeout * time.Duration(time.Second),
	}

	resp, err := client.Get(ipfsTikaURL + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("undesired status '%s' from ipfs-tika", resp.Status)
	}

	// Parse resulting JSON
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return err
	}

	return err
}

// CrawlFile crawls a single object, known to be a file
func (c Crawler) CrawlFile(hash string, name string, parentHash string, parentName string, size uint64) error {
	if size == partialSize && parentHash == "" {
		// Assertion error.
		// REMOVE ME!
		log.Printf("Skipping unreferenced partial content for file %s", hash)
		return nil
	}

	references, alreadyIndexed, err := c.indexReferences(hash, name, parentHash)

	if err != nil {
		return err
	}

	if alreadyIndexed {
		return nil
	}

	log.Printf("Crawling file %s (%s)\n", hash, name)

	metadata := make(map[string]interface{})

	if size > 0 {
		if size > metadataMaxSize {
			// Fail hard for really large files, for now
			return fmt.Errorf("%s (%s) too large, not indexing (for now)", hash, name)
		}

		var path string
		if name != "" && parentHash != "" {
			path = fmt.Sprintf("/ipfs/%s/%s", parentHash, name)
		} else {
			path = fmt.Sprintf("/ipfs/%s", hash)
		}

		tryAgain := true
		for tryAgain {
			err = getMetadata(path, &metadata)

			tryAgain, err = c.handleError(err, hash)

			if tryAgain {
				log.Printf("Retrying in %d seconds", reconnectWait)
				time.Sleep(reconnectWait * time.Duration(time.Second))
			}
		}

		if err != nil {
			return err
		}

		// Check for IPFS links in content
		/*
			for raw_url := range metadata.urls {
				url, err := URL.Parse(raw_url)

				if err != nil {
					return err
				}

				if strings.HasPrefix(url.Path, "/ipfs/") {
					// Found IPFS link!
					args := crawlerArgs{
						Hash:       link.Hash,
						Name:       link.Name,
						Size:       link.Size,
						ParentHash: hash,
					}

				}
			}
		*/
	}

	metadata["size"] = size
	metadata["references"] = references

	err = c.id.IndexItem("file", hash, metadata)
	if err != nil {
		return err
	}

	log.Printf("Finished file %s", hash)

	return nil
}
