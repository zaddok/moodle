package moodle

import (
	"errors"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

var cookieJar *cookiejar.Jar

var ua int = -1
var uaHeaders [][][]string = [][][]string{
	{
		{"DNT", "1"},
		{"Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
		{"User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/603.3.4 (KHTML, like Gecko) Version/10.1.2 Safari/603.3.4"},
		{"Upgrade-Insecure-Requests", "1"},
		{"Accept-Language", "en-au"},
	},
	{
		{"Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8"},
		{"Accept-Language", "en-AU,en;q=0.8,en-US;q=0.6"},
		{"User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/61.0.3163.79 Safari/537.36"},
		{"Upgrade-Insecure-Requests", "1"},
	},
}

type LookupUrl interface {
	GetUrl(url string) (string, int, string, error)
	PostFile(url string, r io.Reader) (string, int, string, error)
}

type DefaultLookupUrl struct {
	client *http.Client
}

// Fetch the content of a URL. Returns the contents, httpStatus, contentType, errorCode.
func (d *DefaultLookupUrl) GetUrl(url string) (string, int, string, error) {
	if d.client == nil {
		netTransport := &http.Transport{
			Dial: (&net.Dialer{
				Timeout: 8 * time.Second,
			}).Dial,
			TLSHandshakeTimeout: 8 * time.Second,
		}

		if cookieJar == nil {
			cookieJar, _ = cookiejar.New(nil)
		}

		d.client = &http.Client{
			Timeout:   time.Second * 16,
			Transport: netTransport,
			Jar:       cookieJar,
		}
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", 0, "", err
	}

	if ua < 0 {
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		ua = r.Intn(len(uaHeaders))
	}
	for _, v := range uaHeaders[ua] {
		req.Header.Set(v[0], v[1])
	}
	//req.Header.Set("Accept-Encoding","gzip, deflate")

	response, err1 := d.client.Get(url)
	if err1 != nil {
		return "", 0, "", err1
	}

	contentType := response.Header.Get("Content-Type")
	if response.StatusCode == 200 &&
		!strings.HasPrefix(contentType, "application/xml") &&
		!strings.HasPrefix(contentType, "application/json") &&
		!strings.HasPrefix(contentType, "application/rss+xml") &&
		!strings.HasPrefix(contentType, "application/atom+xml") &&
		!strings.HasPrefix(contentType, "text/html") &&
		!strings.HasPrefix(contentType, "text/json") &&
		!strings.HasPrefix(contentType, "text/plain") &&
		!strings.HasPrefix(contentType, "text/xml") {
		return "", 0, contentType, errors.New("Ignored non-text response: " + contentType)
	}

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "", 0, "", err
	}

	return strings.TrimSpace(string(body)), response.StatusCode, contentType, nil
}

// PostFile uploads binary content to the specified url
func (d *DefaultLookupUrl) PostFile(url string, r io.Reader) (string, int, string, error) {
	var netTransport = &http.Transport{
		Dial: (&net.Dialer{
			Timeout: 8 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 8 * time.Second,
	}

	if cookieJar == nil {
		cookieJar, _ = cookiejar.New(nil)
	}

	var client = &http.Client{
		Timeout:   time.Second * 16,
		Transport: netTransport,
		Jar:       cookieJar,
	}

	req, err := http.NewRequest("POST", url, r)
	if err != nil {
		return "", 0, "", err
	}

	if ua < 0 {
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		ua = r.Intn(len(uaHeaders))
	}
	for _, v := range uaHeaders[ua] {
		req.Header.Set(v[0], v[1])
	}
	//req.Header.Set("Accept-Encoding","gzip, deflate")

	response, err1 := client.Do(req)
	if err1 != nil {
		return "", 0, "", err1
	}
	defer response.Body.Close()

	contentType := response.Header.Get("Content-Type")
	if response.StatusCode == 200 &&
		!strings.HasPrefix(contentType, "application/xml") &&
		!strings.HasPrefix(contentType, "application/json") &&
		!strings.HasPrefix(contentType, "application/rss+xml") &&
		!strings.HasPrefix(contentType, "application/atom+xml") &&
		!strings.HasPrefix(contentType, "text/html") &&
		!strings.HasPrefix(contentType, "text/json") &&
		!strings.HasPrefix(contentType, "text/plain") &&
		!strings.HasPrefix(contentType, "text/xml") {
		return "", 0, contentType, errors.New("Ignored non-text response: " + contentType)
	}

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "", 0, "", err
	}

	return strings.TrimSpace(string(body)), response.StatusCode, contentType, nil
}
