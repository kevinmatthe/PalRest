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
	req, err := c.request(ctx, http.MethodGet, "/players", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list Palworld players: %w", err)
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	var payload struct {
		Players []struct {
			Name        string `json:"name"`
			AccountName string `json:"accountName"`
			PlayerID    string `json:"playerId"`
			UserID      string `json:"userId"`
		} `json:"players"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes))
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Palworld players: %w", err)
	}
	players := make([]domain.Player, 0, len(payload.Players))
	for _, player := range payload.Players {
		if player.UserID == "" {
			return nil, fmt.Errorf("decode Palworld players: player has empty userId")
		}
		players = append(players, domain.Player{UserID: player.UserID, PlayerID: player.PlayerID, Name: player.Name, AccountName: player.AccountName})
	}
	return players, nil
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
