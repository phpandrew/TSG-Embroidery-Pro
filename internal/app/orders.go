package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phpandrew/TSG-Embroidery-Pro/internal/applog"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/document"
	"github.com/phpandrew/TSG-Embroidery-Pro/internal/orders"
)

// Order lookup: type an order number, fetch its DST files from the shop's
// API and open them with the thread colours the shop has saved for each
// block. The address and keys are entered in Settings by the user; the keys
// are kept encrypted for the Windows user (DPAPI), never in prefs.json.

// OrderPrefs is the non-secret part, kept in prefs.json.
type OrderPrefs struct {
	Enabled     bool   `json:"enabled"`
	BaseURL     string `json:"baseUrl"`
	Machine     string `json:"machine"`
	EmployeeID  string `json:"employeeId"`
	MarkPrinted bool   `json:"markPrinted"`
}

type orderSecrets struct {
	APIKey string `json:"apiKey"`
	AppKey string `json:"appKey"`
	AppID  string `json:"appId"`
}

// OrderSettings is what the Settings tab shows: the keys themselves are
// never sent back to the window, only whether they are set.
type OrderSettings struct {
	OrderPrefs
	HasAPIKey bool   `json:"hasApiKey"`
	HasAppKey bool   `json:"hasAppKey"`
	HasAppID  bool   `json:"hasAppId"`
	Ready     bool   `json:"ready"` // enabled and complete: show the toolbar button
	Missing   string `json:"missing,omitempty"`
}

// OrderSettingsUpdate is what the Settings tab saves. Empty key fields
// leave the stored key unchanged; ClearKeys removes them all.
type OrderSettingsUpdate struct {
	OrderPrefs
	APIKey    string `json:"apiKey"`
	AppKey    string `json:"appKey"`
	AppID     string `json:"appId"`
	ClearKeys bool   `json:"clearKeys"`
}

func (a *App) secretsPath() string {
	if a.cfgDir == "" {
		return ""
	}
	return filepath.Join(a.cfgDir, "order-keys.bin")
}

func (a *App) loadSecrets() orderSecrets {
	var s orderSecrets
	p := a.secretsPath()
	if p == "" {
		return s
	}
	enc, err := os.ReadFile(p)
	if err != nil {
		return s
	}
	plain, err := unprotect(enc)
	if err != nil {
		applog.Errorf("order keys could not be decrypted (saved by another Windows user?)")
		return s
	}
	_ = json.Unmarshal(plain, &s)
	return s
}

func (a *App) saveSecrets(s orderSecrets) error {
	p := a.secretsPath()
	if p == "" {
		return errors.New("no settings folder")
	}
	if s == (orderSecrets{}) {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	plain, _ := json.Marshal(s)
	enc, err := protect(plain)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.cfgDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, enc, 0o600)
}

func (a *App) orderPrefs() OrderPrefs {
	a.prefsMu.Lock()
	defer a.prefsMu.Unlock()
	if a.prefs.Orders == nil {
		return OrderPrefs{}
	}
	return *a.prefs.Orders
}

// GetOrderSettings is shown in Settings → Order lookup.
func (a *App) GetOrderSettings() OrderSettings {
	p := a.orderPrefs()
	s := a.loadSecrets()
	out := OrderSettings{OrderPrefs: p, HasAPIKey: s.APIKey != "", HasAppKey: s.AppKey != "", HasAppID: s.AppID != ""}
	out.Ready = p.Enabled && p.BaseURL != "" && out.HasAPIKey && out.HasAppKey && out.HasAppID
	if p.Enabled {
		var miss []string
		if p.BaseURL == "" {
			miss = append(miss, "API address")
		}
		if !out.HasAPIKey {
			miss = append(miss, "API key")
		}
		if !out.HasAppKey {
			miss = append(miss, "Application key")
		}
		if !out.HasAppID {
			miss = append(miss, "App ID")
		}
		if len(miss) > 0 {
			out.Missing = "Still needs: " + strings.Join(miss, ", ")
		}
	}
	return out
}

// SetOrderSettings saves the connection.
func (a *App) SetOrderSettings(u OrderSettingsUpdate) (OrderSettings, error) {
	if strings.TrimSpace(u.BaseURL) != "" {
		u.BaseURL = orders.NormalizeBase(u.BaseURL)
	} else {
		u.BaseURL = ""
	}
	u.Machine = strings.TrimSpace(u.Machine)
	u.EmployeeID = strings.TrimSpace(u.EmployeeID)
	if u.BaseURL != "" {
		if _, err := orders.New(orders.Config{BaseURL: u.BaseURL, APIKey: "check"}); err != nil {
			return a.GetOrderSettings(), err
		}
	}
	s := a.loadSecrets()
	if u.ClearKeys {
		s = orderSecrets{}
	}
	if v := strings.TrimSpace(u.APIKey); v != "" {
		s.APIKey = v
	}
	if v := strings.TrimSpace(u.AppKey); v != "" {
		s.AppKey = v
	}
	if v := strings.TrimSpace(u.AppID); v != "" {
		s.AppID = v
	}
	if err := a.saveSecrets(s); err != nil {
		return a.GetOrderSettings(), fmt.Errorf("could not store the keys securely: %w", err)
	}
	p := u.OrderPrefs
	a.prefsMu.Lock()
	a.prefs.Orders = &p
	a.prefsMu.Unlock()
	a.savePrefs()
	return a.GetOrderSettings(), nil
}

func (a *App) orderClient() (*orders.Client, error) {
	p := a.orderPrefs()
	if !p.Enabled {
		return nil, errors.New("order lookup is turned off in Settings")
	}
	s := a.loadSecrets()
	return orders.New(orders.Config{
		BaseURL: p.BaseURL, APIKey: s.APIKey, AppKey: s.AppKey, AppID: s.AppID,
		Machine: p.Machine, EmployeeID: p.EmployeeID, MarkPrinted: p.MarkPrinted,
	})
}

// OrderInfo is what the lookup window shows.
type OrderInfo struct {
	Order    *orders.Order    `json:"order"`
	Products *orders.Products `json:"products"`
	Marked   bool             `json:"marked"`            // lookup marked the order printed
	Warning  string           `json:"warning,omitempty"` // the order was found but its files could not be listed
}

// LookupOrder finds the order and its embroidery files.
func (a *App) LookupOrder(number string) (OrderInfo, error) {
	number = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(number), "#"))
	c, err := a.orderClient()
	if err != nil {
		return OrderInfo{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	o, err := c.Lookup(ctx, number)
	if err != nil {
		applog.Infof("order lookup %s: %v", number, err)
		return OrderInfo{}, err
	}
	p, err := c.Products(ctx, o.Number, o.ProductID)
	if err != nil {
		applog.Infof("order %s files: %v", number, err)
		return OrderInfo{Order: o, Products: &orders.Products{}, Marked: a.orderPrefs().MarkPrinted,
			Warning: "The order was found, but its DST files couldn't be fetched: " + err.Error()}, nil
	}
	// second side of a double-sided product
	if o.ProductBack != "" && o.ProductBack != o.ProductID {
		if pb, err := c.Products(ctx, o.Number, o.ProductBack); err == nil {
			seen := map[string]bool{}
			for _, f := range p.Files {
				seen[f.Position] = true
			}
			for _, f := range pb.Files {
				if !seen[f.Position] {
					p.Files = append(p.Files, f)
				}
			}
		}
	}
	// a file without its own colours uses the order's colours for that position
	for i := range p.Files {
		if len(p.Files[i].Threads) == 0 {
			p.Files[i].Threads = o.Colours[p.Files[i].Position]
		}
	}
	applog.Infof("order lookup %s: %d position(s)", o.Number, len(p.Files))
	return OrderInfo{Order: o, Products: p, Marked: a.orderPrefs().MarkPrinted}, nil
}

// OpenOrderFile downloads one position's DST into the orders folder and
// opens it in a new tab with the shop's thread colours.
func (a *App) OpenOrderFile(number string, f orders.File) (document.View, error) {
	if !orders.ValidOrderNumber(number) {
		return a.view(errors.New("invalid order number"))
	}
	if f.Missing || f.URL == "" {
		return a.view(fmt.Errorf("no DST attached for the %s yet", posName(f.Position)))
	}
	c, err := a.orderClient()
	if err != nil {
		return a.view(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	data, err := c.Download(ctx, f.URL)
	if err != nil {
		return a.view(err)
	}
	dir := filepath.Join(a.cfgDir, "orders", number)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return a.view(err)
	}
	p := filepath.Join(dir, number+" "+fileSafe(posName(f.Position))+".dst")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return a.view(err)
	}
	ed := a.newEditor()
	if err := ed.OpenFile(p); err != nil {
		return a.view(err)
	}
	var threads []document.OrderThread
	for _, t := range f.Threads {
		threads = append(threads, document.OrderThread{
			Sequence: t.Sequence, Code: t.Code, Name: t.Name, Color: t.Hex, Brand: t.Brand, Note: t.Note, NotSet: t.NotSet(),
		})
	}
	if err := ed.ApplyOrderThreads(fmt.Sprintf("%s · %s", number, posName(f.Position)), threads); err != nil {
		return a.view(err)
	}
	if a.cur().Blank() {
		a.tabsMu.Lock()
		a.tabs[a.active].ed = ed
		a.tabsMu.Unlock()
	} else {
		a.addTab(ed)
	}
	return a.view(nil)
}

func posName(p string) string {
	switch p {
	case "sleeve_left":
		return "Left sleeve"
	case "sleeve_right":
		return "Right sleeve"
	case "name_plate":
		return "Name plate"
	case "":
		return "Design"
	}
	return strings.ToUpper(p[:1]) + strings.ReplaceAll(p[1:], "_", " ")
}
