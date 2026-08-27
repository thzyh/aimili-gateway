package xui

import (
	"context"
	"errors"
	"net/http/cookiejar"
	"strings"
)

const verifiedAdminContract = "v3-update-user"
const browserSessionCookieName = "3x-ui"

func (c *Client) ProbeAdminCapabilities(ctx context.Context) (AdminCapabilities, error) {
	if c.credentials.TwoFactorCode != "" {
		return AdminCapabilities{ContractVersion: verifiedAdminContract, TOTPCompatible: false}, nil
	}
	if err := c.VerifyAdmin(ctx, c.credentials); err != nil {
		var adapterError *AdapterError
		if errors.As(err, &adapterError) && adapterError.Code == "totp_incompatible" {
			return AdminCapabilities{ContractVersion: verifiedAdminContract, TOTPCompatible: false}, nil
		}
		return AdminCapabilities{}, err
	}
	return AdminCapabilities{
		ContractVersion: verifiedAdminContract,
		CanUpdate:       true,
		CanBridge:       true,
		TOTPCompatible:  true,
	}, nil
}

func (c *Client) VerifyAdmin(ctx context.Context, credentials Credentials) error {
	client, err := c.isolatedAdminClient(credentials)
	if err != nil {
		return err
	}
	if err := client.authenticate(ctx); err != nil {
		return err
	}
	return nil
}

func (c *Client) UpdateAdmin(ctx context.Context, current, next Credentials) error {
	if err := validateAdminCredentials(next); err != nil {
		return err
	}
	client, err := c.isolatedAdminClient(current)
	if err != nil {
		return err
	}
	if err := client.authenticate(ctx); err != nil {
		return err
	}
	payload := map[string]string{
		"oldUsername": current.Username,
		"oldPassword": current.Password,
		"newUsername": next.Username,
		"newPassword": next.Password,
	}
	if _, err := client.call(ctx, "POST", "panel/api/setting/updateUser", payload, false); err != nil {
		return err
	}
	verifier, err := c.isolatedAdminClient(next)
	if err != nil {
		return err
	}
	if err := verifier.authenticate(ctx); err != nil {
		return &AdapterError{Code: "write_verification_failed"}
	}

	c.mu.Lock()
	c.credentials = next
	c.httpClient.Jar = verifier.httpClient.Jar
	c.csrf = verifier.csrf
	c.mu.Unlock()
	return nil
}

func (c *Client) IssueAdminSession(ctx context.Context, credentials Credentials) (BrowserSession, error) {
	client, err := c.isolatedAdminClient(credentials)
	if err != nil {
		return BrowserSession{}, err
	}
	if err := client.authenticate(ctx); err != nil {
		return BrowserSession{}, err
	}
	cookies := client.httpClient.Jar.Cookies(client.baseURL)
	if len(cookies) != 1 || cookies[0].Name != browserSessionCookieName {
		return BrowserSession{}, &AdapterError{Code: "unexpected_cookie"}
	}
	value := cookies[0].Value
	if len(value) < 16 || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n;,") {
		return BrowserSession{}, &AdapterError{Code: "invalid_response"}
	}
	return BrowserSession{CookieName: browserSessionCookieName, Token: []byte(value)}, nil
}

func (c *Client) isolatedAdminClient(credentials Credentials) (*Client, error) {
	if err := validateAdminCredentials(credentials); err != nil {
		return nil, err
	}
	client, err := NewClient(c.baseURL.String(), credentials)
	if err != nil {
		return nil, &AdapterError{Code: "invalid_configuration"}
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, &AdapterError{Code: "session_failed"}
	}
	client.httpClient.Jar = jar
	return client, nil
}

func validateAdminCredentials(credentials Credentials) error {
	if credentials.TwoFactorCode != "" {
		return &AdapterError{Code: "totp_incompatible"}
	}
	if strings.TrimSpace(credentials.Username) == "" || credentials.Username != strings.TrimSpace(credentials.Username) ||
		credentials.Password == "" || credentials.Password != strings.TrimSpace(credentials.Password) {
		return &AdapterError{Code: "invalid_request"}
	}
	return nil
}
