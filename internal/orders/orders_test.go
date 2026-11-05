package orders

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const lookupJSON = `{"response":{"success":{"orders":[{"order":{"order_number":"10099566","status":"Paid","product_id":"40436983","product_id_back":"","double_sided":"false","style":"Gildan 18000B","color":"Navy","size":"Extra Small","qty":"1","admin_message":"","date_printed":"","embroidery_colours":{"front":[{"sequence":1,"thread_code":"5596","thread_name":"Black","hex":"#000000","brand":"Robison-Anton Super Brite Poly","note":""},{"sequence":2,"thread_code":"","thread_name":"","hex":"","brand":"","note":""},{"sequence":3,"thread_code":"5520","thread_name":"Blue","hex":"#003470","brand":"Robison-Anton Super Brite Poly","note":"3D foam"}],"sleeve_right":[{"sequence":1,"thread_code":"5678","thread_name":"Red","hex":"#B81400","brand":"Robison-Anton Super Brite Poly","note":""}]}}}]}}}`

func productsJSON(base string) string {
	return `{"response":{"success":{"products":[{"product":{"product_id":"40436983","order_id":10099566,"title":"Vaughan-Rangers-Crewneck","store":"Vaughan Rangers","file_type":"dst","print_image":"` + base + `/front.dst","print_images_array":[{"position":"front","url":"` + base + `/front.dst","filename":"front.dst","artwork_url":"","dst_missing":false,"thread_colours":[{"sequence":1,"thread_code":"5596","thread_name":"Black","hex":"#000000","brand":"RA","note":""}]},{"position":"sleeve_right","url":"","filename":"","artwork_url":"","dst_missing":true,"thread_colours":[]}],"dst_missing_count":1}}]}}}`
}

func server(t *testing.T, got map[string]map[string]any) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/LookupOrder/"):
			b, _ := io.ReadAll(r.Body)
			m := map[string]any{}
			_ = json.Unmarshal(b, &m)
			got["lookup"] = m
			if f, _ := m["filter"].(map[string]any); f["order_number"] == "404" {
				_, _ = io.WriteString(w, `{"response":{"failure":{"message":"Order not found"}}}`)
				return
			}
			_, _ = io.WriteString(w, lookupJSON)
		case strings.HasSuffix(r.URL.Path, "/GetSellerProducts/"):
			b, _ := io.ReadAll(r.Body)
			m := map[string]any{}
			_ = json.Unmarshal(b, &m)
			got["products"] = m
			_, _ = io.WriteString(w, productsJSON(srv.URL))
		case r.URL.Path == "/front.dst":
			_, _ = w.Write(make([]byte, 600))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLookupProductsDownload(t *testing.T) {
	got := map[string]map[string]any{}
	srv := server(t, got)
	c, err := New(Config{BaseURL: srv.URL + "/api/v2", APIKey: "k-secret", AppKey: "app", AppID: "id1", Machine: "16"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	o, err := c.Lookup(ctx, "10099566")
	if err != nil {
		t.Fatal(err)
	}
	if o.Style != "Gildan 18000B" || o.ProductID != "40436983" || len(o.Colours["front"]) != 3 || len(o.Colours["sleeve_right"]) != 1 {
		t.Fatalf("order = %+v", o)
	}
	if !o.Colours["front"][1].NotSet() || o.Colours["front"][2].Note != "3D foam" || o.Colours["front"][2].Hex != "#003470" {
		t.Fatalf("threads = %+v", o.Colours["front"])
	}
	// not marking printed by default: preview on, update_timestamp off
	f := got["lookup"]["filter"].(map[string]any)
	if f["preview"] != true || f["update_timestamp"] != false || got["lookup"]["preview"] != true || f["machine"] != "16" {
		t.Fatalf("lookup filter = %+v", got["lookup"])
	}
	p, err := c.Products(ctx, o.Number, o.ProductID)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 2 || p.Files[0].Missing || !p.Files[1].Missing || p.Store != "Vaughan Rangers" {
		t.Fatalf("products = %+v", p)
	}
	ex := got["products"]["extra"].(map[string]any)
	if ex["app_id"] != "id1" || ex["master_key"] != "true" || got["products"]["filter"].(map[string]any)["file_type"] != "dst" {
		t.Fatalf("products request = %+v", got["products"])
	}
	data, err := c.Download(ctx, p.Files[0].URL)
	if err != nil || len(data) != 600 {
		t.Fatalf("download: %v %d", err, len(data))
	}
}

func TestFailuresAndValidation(t *testing.T) {
	got := map[string]map[string]any{}
	srv := server(t, got)
	c, _ := New(Config{BaseURL: srv.URL, APIKey: "k"})
	if _, err := c.Lookup(context.Background(), "404"); err == nil || err.Error() != "Order not found" {
		t.Fatalf("want server message, got %v", err)
	}
	if _, err := c.Lookup(context.Background(), "12a"); err == nil {
		t.Fatal("non-digit order numbers must be refused")
	}
	if _, err := New(Config{BaseURL: "http://example.com/api", APIKey: "k"}); err == nil {
		t.Fatal("plain http must be refused")
	}
	if _, err := New(Config{BaseURL: "https://example.com/api"}); err == nil {
		t.Fatal("missing key must be refused")
	}
	if _, err := c.Download(context.Background(), "http://example.com/x.dst"); err == nil {
		t.Fatal("plain http downloads must be refused")
	}
}
