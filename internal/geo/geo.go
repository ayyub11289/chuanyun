// Package geo 查询服务器所在国家和城市，用于节点列表显示。
package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Info 是一个服务器的位置。Code 是两位国家代码（如 JP）。
type Info struct {
	Code    string `json:"code"`
	Country string `json:"country"`
	City    string `json:"city,omitempty"`
}

// Place 是界面上的地名，如「日本 · 东京」。
func (i Info) Place() string {
	if i.Country == "" {
		return ""
	}
	if i.City == "" || i.City == i.Country {
		return i.Country
	}
	return i.Country + " · " + i.City
}

// Endpoint 是查询接口，测试时替换。
var Endpoint = "http://ip-api.com/json/"

// Lookup 查询 host（IP 或域名）的位置。proxy 非空时经该 HTTP 代理访问。
func Lookup(ctx context.Context, host, proxy string) (Info, error) {
	tr := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 10 * time.Second}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	u := Endpoint + url.PathEscape(host) + "?lang=zh-CN&fields=status,message,country,countryCode,city"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Info{}, err
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("位置查询 HTTP %d", resp.StatusCode)
	}
	var r struct {
		Status      string `json:"status"`
		Message     string `json:"message"`
		Country     string `json:"country"`
		CountryCode string `json:"countryCode"`
		City        string `json:"city"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&r); err != nil {
		return Info{}, err
	}
	if r.Status != "success" {
		return Info{}, fmt.Errorf("位置查询失败：%s", r.Message)
	}
	return Info{Code: strings.ToUpper(r.CountryCode), Country: r.Country, City: r.City}, nil
}
