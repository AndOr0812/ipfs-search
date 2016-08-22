package crawler

import (
	"errors"
	"fmt"
	"github.com/dokterbob/ipfs-search/indexer"
	"github.com/dokterbob/ipfs-search/queue"
	"gopkg.in/ipfs/go-ipfs-api.v1"
	"log"
	"net/http"
)

type Crawler struct {
	sh *shell.Shell
	id *indexer.Indexer
	fq *queue.TaskQueue
	hq *queue.TaskQueue
}

func NewCrawler(sh *shell.Shell, id *indexer.Indexer, fq *queue.TaskQueue, hq *queue.TaskQueue) *Crawler {
	c := new(Crawler)
	c.sh = sh
	c.id = id
	c.fq = fq
	c.hq = hq
	return c
}

func hashUrl(hash string) string {
	return fmt.Sprintf("/ipfs/%s", hash)
}

// Given a particular hash, start crawling
func (c Crawler) CrawlHash(hash string) error {
	indexed, err := c.id.IsIndexed(hash)
	if err != nil {
		return err
	}

	if indexed {
		log.Printf("Already indexed '%s', skipping\n", hash)
		return nil
	}

	log.Printf("Crawling hash '%s'\n", hash)

	url := hashUrl(hash)

	list, err := c.sh.FileList(url)
	if err != nil {
		return err
	}

	switch list.Type {
	case "File":
		// Add to file crawl queue
		err = c.fq.AddTask(map[string]interface{}{
			"hash": hash,
		})
		if err != nil {
			// failed to send the task
			return err
		}
	case "Directory":
		// Index name and size for items
		properties := map[string]interface{}{
			"links": list.Links,
			"size":  list.Size,
		}

		c.id.IndexItem("Directory", hash, properties)

		for _, link := range list.Links {
			c.id.IndexReference(link.Type, link.Hash, link.Name, hash)

			switch link.Type {
			case "File":
				// Add file to crawl queue
				err = c.fq.AddTask(map[string]interface{}{
					"hash": link.Hash,
				})
				if err != nil {
					// failed to send the task
					return err
				}

			case "Directory":
				// Add directory to crawl queue
				c.hq.AddTask(map[string]interface{}{
					"hash": link.Hash,
				})
				if err != nil {
					// failed to send the task
					return err
				}
			default:
				log.Printf("Type '%s' skipped for '%s'", list.Type, hash)
			}
		}
	default:
		log.Printf("Type '%s' skipped for '%s'", list.Type, hash)
	}

	return nil
}

func (c Crawler) getMimeType(hash string) (string, error) {
	url := hashUrl(hash)
	response, err := c.sh.Cat(url)
	if err != nil {
		return "", err
	}

	defer response.Close()

	var data []byte
	data = make([]byte, 512)
	numread, err := response.Read(data)
	if err != nil && err.Error() != "EOF" {
		return "", err
	}

	if numread == 0 {
		return "", errors.New("0 characters read, mime type detection failed")
	}

	// Sniffing only uses at most the first 512 bytes
	return http.DetectContentType(data), nil
}

// Crawl a single object, known to be a file
func (c Crawler) CrawlFile(hash string) error {
	log.Printf("Crawling file %s\n", hash)

	mimetype, err := c.getMimeType(hash)
	if err != nil {
		return err
	}

	properties := map[string]interface{}{
		"mimetype": mimetype,
	}

	c.id.IndexItem("File", hash, properties)

	return nil
}
