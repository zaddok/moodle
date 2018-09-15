package moodle

import (
	"context"
	"errors"
	"google.golang.org/appengine/urlfetch"
	"io/ioutil"
	"strings"
)

type GoogleLookupUrl struct {
	Context context.Context
}

func (d *GoogleLookupUrl) GetUrl(url string) (string, int, string, error) {

	client := urlfetch.Client(d.Context)

	response, err1 := client.Get(url)
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
