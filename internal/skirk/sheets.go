package skirk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type SheetsLog struct {
	http          *GoogleHTTPClient
	token         string
	spreadsheetID string
	rangeName     string
}

type SheetRecord struct {
	Name      string
	Data      []byte
	UpdatedNS string
	Action    string
}

func NewSheetsLog(httpClient *GoogleHTTPClient, token string, cfg SheetsConfig) *SheetsLog {
	rangeName := cfg.Range
	if rangeName == "" {
		rangeName = "skirk!A:D"
	}
	return &SheetsLog{http: httpClient, token: token, spreadsheetID: cfg.SpreadsheetID, rangeName: rangeName}
}

func (s *SheetsLog) Put(ctx context.Context, name string, data []byte) error {
	return s.PutMany(ctx, []SheetRecord{{Name: name, Data: data, UpdatedNS: strconv.FormatInt(time.Now().UnixNano(), 10), Action: "put"}})
}

func (s *SheetsLog) PutMany(ctx context.Context, records []SheetRecord) error {
	if len(records) == 0 {
		return nil
	}
	rows := make([][]string, 0, len(records))
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	for _, record := range records {
		updated := record.UpdatedNS
		if updated == "" {
			updated = now
		}
		action := record.Action
		if action == "" {
			action = "put"
		}
		rows = append(rows, []string{record.Name, base64.URLEncoding.EncodeToString(record.Data), updated, action})
	}
	return s.appendRows(ctx, rows)
}

func (s *SheetsLog) Get(ctx context.Context, name string) ([]byte, error) {
	rows, err := s.rows(ctx)
	if err != nil {
		return nil, err
	}
	var latest []string
	for _, row := range rows {
		if len(row) >= 4 && row[0] == name {
			latest = row
		}
	}
	if latest == nil || latest[3] == "delete" {
		return nil, fmt.Errorf("sheet row not found: %s", name)
	}
	return base64.URLEncoding.DecodeString(latest[1])
}

func (s *SheetsLog) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	records, err := s.Entries(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var infos []ObjectInfo
	for _, record := range records {
		if record.Action == "delete" {
			continue
		}
		infos = append(infos, ObjectInfo{Name: record.Name, Size: int64(len(record.Data)), Updated: record.UpdatedNS})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos, nil
}

func (s *SheetsLog) Entries(ctx context.Context, prefix string) ([]SheetRecord, error) {
	rows, err := s.rows(ctx)
	if err != nil {
		return nil, err
	}
	latest := map[string]SheetRecord{}
	for _, row := range rows {
		if len(row) < 4 || !strings.HasPrefix(row[0], prefix) {
			continue
		}
		data, _ := base64.URLEncoding.DecodeString(row[1])
		latest[row[0]] = SheetRecord{Name: row[0], Data: data, UpdatedNS: row[2], Action: row[3]}
	}
	records := make([]SheetRecord, 0, len(latest))
	for _, record := range latest {
		if record.Action != "delete" {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	return records, nil
}

func (s *SheetsLog) Delete(ctx context.Context, name string) error {
	return s.DeleteMany(ctx, []string{name})
}

func (s *SheetsLog) DeleteMany(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	rows := make([][]string, 0, len(names))
	for _, name := range names {
		rows = append(rows, []string{name, "", now, "delete"})
	}
	return s.appendRows(ctx, rows)
}

func (s *SheetsLog) appendRows(ctx context.Context, rows [][]string) error {
	encodedRange := url.PathEscape(s.rangeName)
	values := url.Values{}
	values.Set("valueInputOption", "RAW")
	values.Set("insertDataOption", "INSERT_ROWS")
	body, err := json.Marshal(map[string]any{
		"majorDimension": "ROWS",
		"values":         rows,
	})
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/v4/spreadsheets/%s/values/%s:append?%s", url.PathEscape(s.spreadsheetID), encodedRange, values.Encode())
	result, err := s.http.Request(ctx, http.MethodPost, "sheets.googleapis.com", path, s.jsonHeaders(), body)
	if err != nil {
		return err
	}
	return require2xx(result, "sheets append")
}

func (s *SheetsLog) rows(ctx context.Context) ([][]string, error) {
	encodedRange := url.PathEscape(s.rangeName)
	path := fmt.Sprintf("/v4/spreadsheets/%s/values/%s?majorDimension=ROWS", url.PathEscape(s.spreadsheetID), encodedRange)
	result, err := s.http.Request(ctx, http.MethodGet, "sheets.googleapis.com", path, s.authHeaders(), nil)
	if err != nil {
		return nil, err
	}
	if result.Status == http.StatusNotFound {
		return nil, nil
	}
	if err := require2xx(result, "sheets get"); err != nil {
		return nil, err
	}
	var payload struct {
		Values [][]string `json:"values"`
	}
	if err := json.Unmarshal(result.Body, &payload); err != nil {
		return nil, err
	}
	return payload.Values, nil
}

func (s *SheetsLog) jsonHeaders() map[string]string {
	headers := s.authHeaders()
	headers["Content-Type"] = "application/json"
	return headers
}

func (s *SheetsLog) authHeaders() map[string]string {
	return map[string]string{"Authorization": "Bearer " + s.token}
}
