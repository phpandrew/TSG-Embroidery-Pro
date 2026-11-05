// Package orders talks to the shop's order API (LookupOrder and
// GetSellerProducts) to find an order's embroidery DST files and the thread
// colour for each colour block.
//
// Nothing here knows the API's address or keys: they come from the user's
// settings at run time and are never part of the source code.
package orders

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config is the connection, entered by the user in Settings.
type Config struct {
	BaseURL     string // e.g. https://example.com/api/v2/ - LookupOrder/ and GetSellerProducts/ are appended
	APIKey      string
	AppKey      string // application_key (LookupOrder)
	AppID       string // extra.app_id (GetSellerProducts)
	Machine     string
	EmployeeID  string
	MarkPrinted bool // let the lookup mark the order printed
}

// Thread is the thread for one colour block (sequence starts at 1).
type Thread struct {
	Sequence int    `json:"sequence"`
	Code     string `json:"thread_code"`
	Name     string `json:"thread_name"`
	Hex      string `json:"hex"`
	Brand    string `json:"brand"`
	Note     string `json:"note"`
}

// NotSet reports a block saved without a thread.
func (t Thread) NotSet() bool { return t.Code == "" && t.Name == "" && t.Hex == "" }

// Order is the summary shown before opening files.
type Order struct {
	Number       string              `json:"number"`
	Status       string              `json:"status"`
	ProductID    string              `json:"productId"`
	ProductBack  string              `json:"productBack"`
	Style        string              `json:"style"`
	Color        string              `json:"color"`
	Size         string              `json:"size"`
	Qty          string              `json:"qty"`
	AdminMessage string              `json:"adminMessage"`
	DatePrinted  string              `json:"datePrinted"`
	Colours      map[string][]Thread `json:"colours"` // embroidery_colours, by position
}

// File is one embroidered position of the order.
type File struct {
	Position   string   `json:"position"`
	URL        string   `json:"url"`
	Filename   string   `json:"filename"`
	ArtworkURL string   `json:"artworkUrl"`
	Missing    bool     `json:"missing"` // embroiders, but no DST attached yet
	Threads    []Thread `json:"threads"`
}

// Products is the GetSellerProducts answer for the order.
type Products struct {
	Title   string `json:"title"`
	Store   string `json:"store"`
	Files   []File `json:"files"`
	NoDST   bool   `json:"noDst"` // nothing on the product is embroidered
	DSTNote string `json:"dstNote"`
}

var orderRe = regexp.MustCompile(`^[0-9]{1,12}$`)

// ValidOrderNumber reports whether s looks like an order number.
func ValidOrderNumber(s string) bool { return orderRe.MatchString(s) }

const (
	maxJSON = 4 << 20
	maxDST  = 64 << 20
)

// Client calls the API.
type Client struct {
	cfg  Config
	http *http.Client
}

// New checks the config and returns a client.
func New(cfg Config) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || u.Host == "" {
		return nil, errors.New("set the API address in Settings → Order lookup")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")) {
		return nil, errors.New("the API address must start with https://")
	}
	if cfg.APIKey == "" {
		return nil, errors.New("set the API key in Settings → Order lookup")
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	cfg.BaseURL = u.String()
	c := &Client{cfg: cfg, http: &http.Client{Timeout: 45 * time.Second}}
	// never follow a redirect to plain http (keys are in the request body)
	c.http.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" && u.Scheme == "https" {
			return errors.New("refusing a redirect away from https")
		}
		return nil
	}
	return c, nil
}

// flex reads a JSON string, number or bool as a string.
type flex string

func (f *flex) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*f = ""
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flex(s)
		return nil
	}
	*f = flex(string(b))
	return nil
}

func truthy(f flex) bool {
	s := strings.ToLower(string(f))
	return s == "true" || s == "1" || s == "yes"
}

// envelope is {"response":{"success":{...}} | {"failure":{"message":...}}}.
type envelope struct {
	Response struct {
		Success json.RawMessage `json:"success"`
		Failure *struct {
			Message string `json:"message"`
		} `json:"failure"`
	} `json:"response"`
}

func (c *Client) post(ctx context.Context, endpoint string, body any) (json.RawMessage, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+endpoint+"/", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "TSG-Embroidery-Pro")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", endpoint, redact(err, c.cfg))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxJSON+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxJSON {
		return nil, fmt.Errorf("%s: answer too large", endpoint)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s: server returned %s", endpoint, resp.Status)
		}
		return nil, fmt.Errorf("%s: unexpected answer from the server", endpoint)
	}
	if env.Response.Failure != nil {
		msg := strings.TrimSpace(env.Response.Failure.Message)
		if msg == "" {
			msg = "request failed"
		}
		return nil, errors.New(msg)
	}
	if len(env.Response.Success) == 0 {
		return nil, fmt.Errorf("%s: server returned %s with no result", endpoint, resp.Status)
	}
	return env.Response.Success, nil
}

// redact removes the keys from an error message, just in case.
func redact(err error, cfg Config) error {
	msg := err.Error()
	for _, k := range []string{cfg.APIKey, cfg.AppKey, cfg.AppID} {
		if len(k) >= 4 {
			msg = strings.ReplaceAll(msg, k, "•••")
		}
	}
	return errors.New(msg)
}

// Lookup calls LookupOrder.
func (c *Client) Lookup(ctx context.Context, number string) (*Order, error) {
	if !ValidOrderNumber(number) {
		return nil, errors.New("order numbers are digits only")
	}
	filter := map[string]any{
		"order_number":     number,
		"update_timestamp": c.cfg.MarkPrinted,
		"auto_script":      false,
		"preview":          !c.cfg.MarkPrinted,
	}
	if c.cfg.Machine != "" {
		filter["machine"] = c.cfg.Machine
	}
	if c.cfg.EmployeeID != "" {
		filter["employee_id"] = c.cfg.EmployeeID
	}
	body := map[string]any{"api_key": c.cfg.APIKey, "application_key": c.cfg.AppKey, "filter": filter}
	if !c.cfg.MarkPrinted {
		body["preview"] = true
	}
	raw, err := c.post(ctx, "LookupOrder", body)
	if err != nil {
		return nil, err
	}
	var succ struct {
		Orders []struct {
			Order struct {
				OrderNumber  flex            `json:"order_number"`
				Status       flex            `json:"status"`
				ProductID    flex            `json:"product_id"`
				ProductBack  flex            `json:"product_id_back"`
				Style        flex            `json:"style"`
				Color        flex            `json:"color"`
				Size         flex            `json:"size"`
				Qty          flex            `json:"qty"`
				AdminMessage flex            `json:"admin_message"`
				DatePrinted  flex            `json:"date_printed"`
				Colours      json.RawMessage `json:"embroidery_colours"`
			} `json:"order"`
		} `json:"orders"`
	}
	if err := json.Unmarshal(raw, &succ); err != nil {
		return nil, fmt.Errorf("LookupOrder: unexpected answer: %w", err)
	}
	if len(succ.Orders) == 0 {
		return nil, errors.New("order not found")
	}
	o := succ.Orders[0].Order
	out := &Order{
		Number: string(o.OrderNumber), Status: string(o.Status), ProductID: string(o.ProductID),
		ProductBack: string(o.ProductBack), Style: string(o.Style), Color: string(o.Color),
		Size: string(o.Size), Qty: string(o.Qty), AdminMessage: string(o.AdminMessage),
		DatePrinted: string(o.DatePrinted), Colours: map[string][]Thread{},
	}
	if out.Number == "" {
		out.Number = number
	}
	// embroidery_colours is an object keyed by position; {} or [] when none
	if len(o.Colours) > 0 && o.Colours[0] == '{' {
		var m map[string][]threadJSON
		if err := json.Unmarshal(o.Colours, &m); err == nil {
			for pos, list := range m {
				out.Colours[pos] = convert(list)
			}
		}
	}
	return out, nil
}

type threadJSON struct {
	Sequence flex `json:"sequence"`
	Code     flex `json:"thread_code"`
	Name     flex `json:"thread_name"`
	Hex      flex `json:"hex"`
	Brand    flex `json:"brand"`
	Note     flex `json:"note"`
}

var hexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func convert(list []threadJSON) []Thread {
	out := make([]Thread, 0, len(list))
	for _, t := range list {
		n, _ := strconv.Atoi(string(t.Sequence))
		if n < 1 || n > 999 {
			continue
		}
		hex := strings.TrimSpace(string(t.Hex))
		if hex != "" && !hexRe.MatchString(hex) {
			hex = ""
		}
		out = append(out, Thread{
			Sequence: n,
			Code:     clean(string(t.Code), 32), Name: clean(string(t.Name), 60),
			Hex: strings.ToLower(hex), Brand: clean(string(t.Brand), 80), Note: clean(string(t.Note), 120),
		})
	}
	return out
}

// clean trims and limits text from the server before it reaches the UI.
func clean(s string, max int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 32 {
			return ' '
		}
		return r
	}, s))
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

// Products calls GetSellerProducts for the order's DST files.
func (c *Client) Products(ctx context.Context, number, productID string) (*Products, error) {
	if c.cfg.AppID == "" {
		return nil, errors.New("set the App ID in Settings → Order lookup")
	}
	body := map[string]any{
		"api_key": c.cfg.APIKey,
		"filter":  map[string]any{"order_id": number, "product_id": productID, "file_type": "dst"},
		"extra":   map[string]any{"app_id": c.cfg.AppID, "master_key": "true"},
	}
	raw, err := c.post(ctx, "GetSellerProducts", body)
	if err != nil {
		return nil, err
	}
	var succ struct {
		Products []struct {
			Product struct {
				Title        flex  `json:"title"`
				Store        flex  `json:"store"`
				DSTAvailable *bool `json:"dst_available"`
				DSTNote      flex  `json:"dst_note"`
				PrintImage   flex  `json:"print_image"`
				Images       []struct {
					Position   flex         `json:"position"`
					URL        flex         `json:"url"`
					Filename   flex         `json:"filename"`
					ArtworkURL flex         `json:"artwork_url"`
					Missing    flex         `json:"dst_missing"`
					Threads    []threadJSON `json:"thread_colours"`
				} `json:"print_images_array"`
			} `json:"product"`
		} `json:"products"`
	}
	if err := json.Unmarshal(raw, &succ); err != nil {
		return nil, fmt.Errorf("GetSellerProducts: unexpected answer: %w", err)
	}
	out := &Products{Files: []File{}}
	for _, p := range succ.Products {
		pr := p.Product
		if out.Title == "" {
			out.Title, out.Store = clean(string(pr.Title), 120), clean(string(pr.Store), 120)
		}
		if pr.DSTAvailable != nil && !*pr.DSTAvailable {
			out.NoDST = true
			out.DSTNote = clean(string(pr.DSTNote), 200)
			continue
		}
		for _, im := range pr.Images {
			f := File{
				Position: clean(string(im.Position), 40), URL: strings.TrimSpace(string(im.URL)),
				Filename: clean(string(im.Filename), 120), ArtworkURL: strings.TrimSpace(string(im.ArtworkURL)),
				Missing: truthy(im.Missing), Threads: convert(im.Threads),
			}
			if f.URL == "" {
				f.Missing = true
			}
			if f.Position == "" {
				f.Position = "front"
			}
			out.Files = append(out.Files, f)
		}
		if len(pr.Images) == 0 && string(pr.PrintImage) != "" {
			out.Files = append(out.Files, File{Position: "front", URL: string(pr.PrintImage), Filename: "design.dst"})
		}
	}
	return out, nil
}

// Download fetches a DST file (https only, size-capped). The keys are not
// sent: file links are plain downloads.
func (c *Client) Download(ctx context.Context, link string) ([]byte, error) {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" || u.Scheme != "https" {
		if !(err == nil && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")) {
			return nil, errors.New("the DST link is not a secure https address")
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TSG-Embroidery-Pro")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, redact(err, c.cfg)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDST+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDST {
		return nil, errors.New("DST file is too large")
	}
	if len(data) < 512 {
		return nil, errors.New("downloaded file is not a DST (too small)")
	}
	return data, nil
}
