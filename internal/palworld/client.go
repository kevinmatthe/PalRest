package palworld

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

const maxResponseBytes = 1 << 20

type Client struct {
	baseURL  string
	password string
	http     *http.Client
}

func New(baseURL, password string, timeout time.Duration) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		password: password,
		http:     &http.Client{Timeout: timeout},
	}
}

func (c *Client) ListPlayers(ctx context.Context) ([]domain.Player, error) {
	var payload struct {
		Players []struct {
			Name          string  `json:"name"`
			AccountName   string  `json:"accountName"`
			PlayerID      string  `json:"playerId"`
			UserID        string  `json:"userId"`
			IP            string  `json:"ip"`
			Ping          float64 `json:"ping"`
			LocationX     float64 `json:"location_x"`
			LocationY     float64 `json:"location_y"`
			Level         int     `json:"level"`
			BuildingCount int     `json:"building_count"`
		} `json:"players"`
	}
	if err := c.getJSON(ctx, "/players", &payload, false); err != nil {
		return nil, fmt.Errorf("list Palworld players: %w", err)
	}
	players := make([]domain.Player, 0, len(payload.Players))
	for _, player := range payload.Players {
		if player.UserID == "" {
			return nil, fmt.Errorf("decode Palworld players: player has empty userId")
		}
		players = append(players, domain.Player{
			UserID: player.UserID, PlayerID: player.PlayerID, Name: player.Name, AccountName: player.AccountName,
			IP: player.IP, Ping: player.Ping, LocationX: player.LocationX, LocationY: player.LocationY,
			Level: player.Level, BuildingCount: player.BuildingCount,
		})
	}
	return players, nil
}

func (c *Client) Metrics(ctx context.Context) (domain.ServerMetrics, error) {
	var metrics domain.ServerMetrics
	if err := c.getJSON(ctx, "/metrics", &metrics, false); err != nil {
		return domain.ServerMetrics{}, fmt.Errorf("get Palworld metrics: %w", err)
	}
	return metrics, nil
}

func (c *Client) Info(ctx context.Context) (domain.ServerInfo, error) {
	var info domain.ServerInfo
	if err := c.getJSON(ctx, "/info", &info, false); err != nil {
		return domain.ServerInfo{}, fmt.Errorf("get Palworld info: %w", err)
	}
	return info, nil
}

func (c *Client) Settings(ctx context.Context) (domain.ServerSettings, error) {
	var values map[string]any
	if err := c.getJSON(ctx, "/settings", &values, true); err != nil {
		return domain.ServerSettings{}, fmt.Errorf("get Palworld settings: %w", err)
	}
	return domain.ServerSettings{Values: values}, nil
}

func (c *Client) getJSON(ctx context.Context, path string, destination any, useNumber bool) error {
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Palworld request %s: %w", path, err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Palworld response %s: %w", path, err)
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("Palworld response %s exceeds %d bytes", path, maxResponseBytes)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("decode Palworld response %s: expected top-level JSON object", path)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if useNumber {
		decoder.UseNumber()
	}
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode Palworld response %s: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode Palworld response %s: trailing JSON value", path)
		}
		return fmt.Errorf("decode Palworld response %s: trailing data", path)
	}
	return nil
}

func (c *Client) Announce(ctx context.Context, message string) error {
	return c.post(ctx, "/announce", map[string]string{"message": message})
}

func (c *Client) Kick(ctx context.Context, userID, message string) error {
	return c.post(ctx, "/kick", map[string]string{"userid": userID, "message": message})
}

func (c *Client) post(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Palworld request: %w", err)
	}
	req, err := c.request(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Palworld request %s: %w", path, err)
	}
	defer resp.Body.Close()
	return checkStatus(resp)
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create Palworld request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth("admin", c.password)
	return req, nil
}

func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("Palworld API returned %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
}
