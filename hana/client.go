package hana

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/imroc/req"
)

const keyCSRFTokenHeader = "x-csrf-token"

const keyAuthorization = "Authorization"

const valueRequired = "required"

// Client type
type Client struct {
	uri           *url.URL
	token         string
	req           *req.Req
	sslVerify     bool
	baseDirectory string
}

func (c *Client) formatURI(path string) string {
	port := c.uri.Port()
	// default to 443 port
	if len(port) == 0 {
		port = "443"
	}
	return fmt.Sprintf("https://%s:%s%s", c.uri.Host, port, path)
}

func isCSRFTokenError(response *http.Response) bool {
	return (response.StatusCode == http.StatusForbidden &&
		strings.ToLower(response.Header.Get(keyCSRFTokenHeader)) == valueRequired)
}

func (c *Client) request(method, path string, infos ...interface{}) (*req.Resp, error) {

	// format url
	url := c.formatURI(path)

	password, _ := c.uri.User.Password()

	header := req.Header{
		keyCSRFTokenHeader: c.token,
		keyAuthorization:   basicAuth(c.uri.User.Username(), password),
	}

	infos = append(infos, header)

	// do request
	resp, err := req.Do(method, url, infos...)

	if isCSRFTokenError(resp.Response()) {
		// try refresh csrf token
		if err := c.fetchCSRFToken(); err != nil {
			return nil, err
		}
		// re process request
		resp, err = req.Do(method, url, infos...)
	}

	if err != nil {
		return nil, err
	}

	return resp, err

}

func (c *Client) fetchCSRFToken() error {

	password, _ := c.uri.User.Password()
	header := req.Header{
		keyCSRFTokenHeader: "fetch",
		keyAuthorization:   basicAuth(c.uri.User.Username(), password),
	}

	resp, err := c.req.Head(c.formatURI("/sap/hana/xs/dt/base/info"), header)

	if err != nil {
		return err
	}

	httpResponse := resp.Response()

	status := httpResponse.StatusCode

	token := httpResponse.Header.Get("x-csrf-token")

	if len(token) != 0 && token != "unsafe" {
		c.token = token
	} else {
		switch {
		case 300 <= status && status < 400:
			return errors.New("redirect, please check your credential")
		case 400 <= status && status < 500:
			return errors.New("request is not accepted")
		case 500 <= status && status < 600:
			return errors.New("server is down")
		default:
			return errors.New("could not fetch csrf token, please check your credential")
		}
	}

	return nil
}

func (c *Client) checkCredential() error {
	if err := c.fetchCSRFToken(); err != nil {
		return err
	}
	return nil
}

func (c *Client) checkURIValidate(uri *url.URL) error {
	_, err := net.LookupHost(uri.Host)
	if err != nil {
		return err
	}
	return nil
}

// ReadFile content
func (c *Client) ReadFile(filePath string) ([]byte, error) {
	res, err := c.request(
		"GET",
		fmt.Sprintf("/sap/hana/xs/dt/base/file%s", filePath),
	)

	if err != nil {
		return nil, err
	}

	if res.Response().StatusCode == 404 {
		return nil, ErrFileNotFound
	}

	return res.ToBytes()
}

// ReadDirectory information
func (c *Client) ReadDirectory(filePath string) (*DirectoryDetail, error) {

	res, err := c.request(
		"GET",
		fmt.Sprintf("/sap/hana/xs/dt/base/file%s", filePath),
		req.QueryParam{"depth": 1},
	)

	if err != nil {
		return nil, err
	}

	if res.Response().StatusCode == 404 {
		return nil, ErrFileNotFound
	}

	rt := &DirectoryDetail{}

	if err := res.ToJSON(rt); err != nil {
		return nil, err
	}

	return rt, nil

}

// Stat func
func (c *Client) Stat(filePath string) (*PathStat, error) {

	rt := &PathStat{}

	query := req.QueryParam{
		"depth": 0,
		"parts": "meta",
	}

	res, err := c.request(
		"GET",
		fmt.Sprintf("/sap/hana/xs/dt/base/file%s", filePath),
		query,
	)

	if err != nil {
		return nil, err
	}

	if res.Response().StatusCode == 404 {
		return nil, ErrFileNotFound
	}

	body, err := res.ToString()

	if gjson.Get(body, "Directory").Bool() {

		dir := &DirectoryMeta{}
		if err := json.Unmarshal([]byte(body), dir); err != nil {
			return nil, err
		}

		rt.Directory = dir.Directory
		rt.Executable = dir.Attributes.Executable
		rt.Archive = dir.Attributes.Archive
		rt.Hidden = dir.Attributes.Hidden
		rt.ReadOnly = dir.Attributes.ReadOnly
		rt.SymbolicLink = dir.Attributes.SymbolicLink
		rt.TimeStamp = dir.LocalTimeStamp

	} else {
		f := &File{}

		if err := json.Unmarshal([]byte(body), f); err != nil {
			return nil, err
		}

		rt.Directory = f.Directory
		rt.Executable = f.Attributes.Executable
		rt.Archive = f.Attributes.Archive
		rt.Hidden = f.Attributes.Hidden
		rt.ReadOnly = f.Attributes.ReadOnly
		rt.SymbolicLink = f.Attributes.SymbolicLink
		rt.Activated = f.Attributes.SapBackPack.Activated

		rt.TimeStamp = f.LocalTimeStamp

	}

	return rt, nil
}

// NewClient for hana
func NewClient(uri *url.URL) (*Client, error) {
	rt := &Client{uri: uri, req: req.New(), baseDirectory: uri.Path, sslVerify: true}

	if err := rt.checkURIValidate(uri); err != nil {
		return nil, err
	}

	if err := rt.checkCredential(); err != nil {
		return nil, err
	}

	return rt, nil
}
