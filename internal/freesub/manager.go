package freesub

// Manager owns exactly one freesub backup runtime. It intentionally does not
// use the regular proxy-group orchestrator or AimiliVPN slots.
import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/thzyh/aimili-gateway/internal/adapters/xui"
	"github.com/thzyh/aimili-gateway/internal/domain"
	"github.com/thzyh/aimili-gateway/internal/store"
)

type ManagerConfig struct {
	FeedPath, SingBoxPath, StateDir string
	SocksPort, VLESSPort, MixedPort int
	RealityServerName               string
	XUI                             *xui.Client
	RefreshSubscription             func(context.Context) error
}

type Manager struct {
	cfg       ManagerConfig
	store     *store.Store
	masterKey []byte
	mu        sync.Mutex
	process   *exec.Cmd
}

func NewManager(cfg ManagerConfig, database *store.Store, masterKey []byte) (*Manager, error) {
	if database == nil || cfg.XUI == nil || cfg.SocksPort < 1 || cfg.VLESSPort < 1 || cfg.MixedPort < 1 || strings.TrimSpace(cfg.FeedPath) == "" || strings.TrimSpace(cfg.SingBoxPath) == "" || strings.TrimSpace(cfg.StateDir) == "" {
		return nil, errors.New("invalid freesub manager configuration")
	}
	return &Manager{cfg: cfg, store: database, masterKey: append([]byte(nil), masterKey...)}, nil
}

// Recover restarts only the persisted freesub runtime. Waiting-manual records
// remain untouched so a Gateway or VPS restart cannot reset the repair count.
func (m *Manager) Recover(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.store.GetFreesubBackupConfig(ctx, m.masterKey)
	if errors.Is(err, store.ErrFreesubBackupNotFound) || (err == nil && c.Status != domain.FreesubBackupReady) {
		return nil
	}
	if err != nil {
		return err
	}
	var config map[string]any
	if err := json.Unmarshal(c.CandidateConfig, &config); err != nil {
		return errors.New("decode persisted freesub candidate")
	}
	candidate := Candidate{CandidateID: c.CandidateID, Country: c.CountryCode, Protocol: c.Protocol, ExitIP: c.ExitIP, Config: config}
	if err := m.activateCandidate(ctx, &c, candidate); err != nil {
		c.Status = domain.FreesubBackupDegraded
		c.RuntimePID = 0
		c.SocksPort = 0
		c.LastErrorCode = "runtime_recovery_failed"
		if saveErr := m.store.PutFreesubBackup(ctx, c, c.Version, m.masterKey); saveErr != nil {
			return errors.Join(err, saveErr)
		}
		return err
	}
	return m.store.PutFreesubBackup(ctx, c, c.Version, m.masterKey)
}

func (m *Manager) Summary(ctx context.Context) (domain.FreesubBackupConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.store.GetFreesubBackup(ctx)
	if errors.Is(err, store.ErrFreesubBackupNotFound) {
		return domain.FreesubBackupConnection{ID: "agw-freesub", Status: domain.FreesubBackupStandby}, nil
	}
	return c, err
}

func (m *Manager) Check(ctx context.Context) (domain.FreesubBackupConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.store.GetFreesubBackupConfig(ctx, m.masterKey)
	if errors.Is(err, store.ErrFreesubBackupNotFound) {
		return m.provisionLocked(ctx)
	}
	if err != nil {
		return c, err
	}
	if c.Status == domain.FreesubBackupWaitingManual {
		return c, errors.New("freesub backup is waiting for manual recovery")
	}
	if c.SocksPort == 0 || !m.processAlive(c.RuntimePID) {
		c.Status = domain.FreesubBackupDegraded
		c.LastErrorCode = "runtime_unavailable"
		c.LastCheckedAt = time.Now().UTC()
		if saveErr := m.store.PutFreesubBackup(ctx, c, c.Version, m.masterKey); saveErr != nil {
			return c, saveErr
		}
		c.Version++
		return c, errors.New("freesub backup runtime unavailable")
	}
	exit, probeErr := probeViaSOCKS(ctx, c.SocksPort)
	c.LastCheckedAt = time.Now().UTC()
	if probeErr != nil {
		c.Status = domain.FreesubBackupDegraded
		c.LastErrorCode = "probe_failed"
	} else {
		c.Status = domain.FreesubBackupReady
		c.ExitIP = exit
		c.LastErrorCode = ""
	}
	if saveErr := m.store.PutFreesubBackup(ctx, c, c.Version, m.masterKey); saveErr != nil {
		return c, saveErr
	}
	c.Version++
	if probeErr != nil && c.RepairAttempts == 0 {
		return m.replaceLocked(ctx, c)
	}
	if c.Status == domain.FreesubBackupReady && m.cfg.RefreshSubscription != nil {
		_ = m.cfg.RefreshSubscription(ctx)
	}
	if probeErr != nil {
		return c, probeErr
	}
	return c, nil
}

func (m *Manager) Replace(ctx context.Context) (domain.FreesubBackupConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.store.GetFreesubBackupConfig(ctx, m.masterKey)
	if err != nil {
		return c, err
	}
	return m.replaceLocked(ctx, c)
}

func (m *Manager) replaceLocked(ctx context.Context, c domain.FreesubBackupConnection) (domain.FreesubBackupConnection, error) {
	if c.Status != domain.FreesubBackupDegraded || c.RepairAttempts != 0 {
		return c, errors.New("automatic replacement is not available")
	}
	fingerprint := c.FailureFingerprint
	if fingerprint == "" {
		fingerprint = c.CandidateID + ":" + c.LastErrorCode
	}
	if err := c.BeginAutoReplacement(fingerprint); err != nil {
		return c, err
	}
	// Persist the one-shot counter before any process or x-ui mutation.
	if err := m.store.PutFreesubBackup(ctx, c, c.Version, m.masterKey); err != nil {
		return c, err
	}
	c.Version++
	feed, err := Load(m.cfg.FeedPath)
	if err != nil {
		return m.failReplacement(ctx, c, "feed_unavailable", err)
	}
	next, err := feed.SameCountry(c.CountryCode, c.CandidateID)
	if err != nil {
		return m.failReplacement(ctx, c, "no_same_country_candidate", err)
	}
	oldPID := c.RuntimePID
	if oldPID > 0 {
		m.stopPID(oldPID)
	}
	if err := m.activateCandidate(ctx, &c, next); err != nil {
		return m.failReplacement(ctx, c, "candidate_start_failed", err)
	}
	if err := m.store.PutFreesubBackup(ctx, c, c.Version, m.masterKey); err != nil {
		return c, err
	}
	c.Version++
	if m.cfg.RefreshSubscription != nil {
		_ = m.cfg.RefreshSubscription(ctx)
	}
	return c, nil
}

func (m *Manager) failReplacement(ctx context.Context, c domain.FreesubBackupConnection, code string, cause error) (domain.FreesubBackupConnection, error) {
	// The one-shot reservation was persisted before candidate activation and
	// advanced the optimistic-lock version. Reload it before recording the
	// terminal state so a failed replacement cannot remain provisioning.
	if latest, err := m.store.GetFreesubBackupConfig(ctx, m.masterKey); err == nil {
		c = latest
	}
	c.Status = domain.FreesubBackupWaitingManual
	c.LastErrorCode = code
	if err := m.store.PutFreesubBackup(ctx, c, c.Version, m.masterKey); err != nil {
		return c, errors.Join(cause, fmt.Errorf("persist waiting-manual state: %w", err))
	}
	c.Version++
	return c, cause
}

// ManualProvision starts a new failure cycle only after an operator explicitly
// requests it. Unlike automatic replacement it may choose a different country,
// but it still probes a bounded list serially and runs only one candidate.
func (m *Manager) ManualProvision(ctx context.Context) (domain.FreesubBackupConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.store.GetFreesubBackupConfig(ctx, m.masterKey)
	if err != nil {
		return current, err
	}
	if current.Status != domain.FreesubBackupWaitingManual && current.Status != domain.FreesubBackupRepairRequired {
		return current, errors.New("manual freesub recovery is not available")
	}
	feed, err := Load(m.cfg.FeedPath)
	if err != nil {
		return current, err
	}
	candidates := feed.InitialCandidates(12)
	if len(candidates) == 0 {
		return current, ErrNoSameCountryCandidate
	}
	if m.processAlive(current.RuntimePID) {
		m.stopPID(current.RuntimePID)
	}
	var activationErr error
	for _, candidate := range candidates {
		next := current
		next.Status = domain.FreesubBackupProvisioning
		if activationErr = m.activateCandidate(ctx, &next, candidate); activationErr != nil {
			continue
		}
		if err := m.ensurePublic(ctx, &next); err != nil {
			m.stopPID(next.RuntimePID)
			return current, err
		}
		next.RepairAttempts = 0
		next.FailureFingerprint = ""
		next.LastErrorCode = ""
		if err := m.store.PutFreesubBackup(ctx, next, current.Version, m.masterKey); err != nil {
			m.stopPID(next.RuntimePID)
			return current, err
		}
		next.Version++
		if m.cfg.RefreshSubscription != nil {
			_ = m.cfg.RefreshSubscription(ctx)
		}
		return next, nil
	}
	return current, activationErr
}

func (m *Manager) provisionLocked(ctx context.Context) (domain.FreesubBackupConnection, error) {
	feed, err := Load(m.cfg.FeedPath)
	if err != nil {
		return domain.FreesubBackupConnection{ID: "agw-freesub", Status: domain.FreesubBackupStandby}, err
	}
	candidates := feed.InitialCandidates(12)
	if len(candidates) == 0 {
		return domain.FreesubBackupConnection{ID: "agw-freesub", Status: domain.FreesubBackupStandby}, ErrNoSameCountryCandidate
	}
	var c domain.FreesubBackupConnection
	var activationErr error
	for _, candidate := range candidates {
		c = domain.FreesubBackupConnection{ID: "agw-freesub", CandidateID: candidate.CandidateID, CountryCode: candidate.Country, Protocol: candidate.Protocol, CandidateIP: candidateServer(candidate), Status: domain.FreesubBackupProvisioning, Version: 1, CandidateConfig: mustJSON(candidate.Config), UpdatedAt: time.Now().UTC()}
		if activationErr = m.activateCandidate(ctx, &c, candidate); activationErr == nil {
			break
		}
	}
	if activationErr != nil {
		c.Status = domain.FreesubBackupDegraded
		c.LastErrorCode = "candidate_start_failed"
		if saveErr := m.store.PutFreesubBackup(ctx, c, 0, m.masterKey); saveErr != nil {
			return c, errors.Join(activationErr, saveErr)
		}
		return c, activationErr
	}
	if err := m.ensurePublic(ctx, &c); err != nil {
		m.stopPID(c.RuntimePID)
		c.Status = domain.FreesubBackupDegraded
		c.LastErrorCode = "xui_provision_failed"
		if saveErr := m.store.PutFreesubBackup(ctx, c, 0, m.masterKey); saveErr != nil {
			return c, errors.Join(err, saveErr)
		}
		return c, err
	}
	if err := m.store.PutFreesubBackup(ctx, c, 0, m.masterKey); err != nil {
		m.stopPID(c.RuntimePID)
		return c, err
	}
	c.Version = 1
	if m.cfg.RefreshSubscription != nil {
		_ = m.cfg.RefreshSubscription(ctx)
	}
	return c, nil
}

func candidateServer(candidate Candidate) string {
	server, _ := candidate.Config["server"].(string)
	return server
}

func (m *Manager) ensurePublic(ctx context.Context, c *domain.FreesubBackupConnection) error {
	vlessID, err := m.secret(ctx, "freesub-vless-client", 16)
	if err != nil {
		return err
	}
	username, err := m.secret(ctx, "freesub-mixed-user", 8)
	if err != nil {
		return err
	}
	password, err := m.secret(ctx, "freesub-mixed-pass", 24)
	if err != nil {
		return err
	}
	desired := xui.DesiredGroup{ResourceName: "agw-freesub", SOCKSPort: c.SocksPort, VLESSPort: m.cfg.VLESSPort, MixedPort: m.cfg.MixedPort, VLESSClientID: string(vlessID), MixedUsername: string(username), MixedPassword: string(password), RealityTarget: "127.0.0.1:443", RealityServerName: m.cfg.RealityServerName}
	managed, err := m.cfg.XUI.EnsureManagedGroup(ctx, desired)
	if err != nil {
		return err
	}
	c.XUIInboundID = managed.VLESSInboundID
	c.PublicPort = m.cfg.VLESSPort
	return nil
}

func (m *Manager) secret(ctx context.Context, purpose string, size int) ([]byte, error) {
	value, err := m.store.GetCredential(ctx, purpose, m.masterKey)
	if err == nil {
		if strings.Contains(purpose, "client") {
			normalized, normalizeErr := normalizeUUID(value)
			if normalizeErr != nil {
				return nil, normalizeErr
			}
			if string(normalized) != string(value) {
				if err := m.store.PutCredential(ctx, purpose, normalized, m.masterKey); err != nil {
					return nil, err
				}
			}
			return normalized, nil
		}
		return value, nil
	}
	if !errors.Is(err, store.ErrCredentialNotFound) {
		return nil, err
	}
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	if strings.Contains(purpose, "client") {
		raw[6] = raw[6]&0x0f | 0x40
		raw[8] = raw[8]&0x3f | 0x80
		value = []byte(formatUUID(raw))
	} else if strings.Contains(purpose, "user") {
		value = []byte("agw-freesub-" + hex.EncodeToString(raw[:4]))
	} else {
		value = raw
	}
	if err := m.store.PutCredential(ctx, purpose, value, m.masterKey); err != nil {
		return nil, err
	}
	return value, nil
}

func normalizeUUID(value []byte) ([]byte, error) {
	if len(value) == 36 && value[8] == '-' && value[13] == '-' && value[18] == '-' && value[23] == '-' {
		if decoded, err := hex.DecodeString(strings.ReplaceAll(string(value), "-", "")); err == nil && len(decoded) == 16 {
			return append([]byte(nil), value...), nil
		}
		return nil, errors.New("invalid stored freesub client uuid")
	}
	if len(value) != 32 {
		return nil, errors.New("invalid stored freesub client uuid")
	}
	raw, err := hex.DecodeString(string(value))
	if err != nil || len(raw) != 16 {
		return nil, errors.New("invalid stored freesub client uuid")
	}
	return []byte(formatUUID(raw)), nil
}

func formatUUID(raw []byte) string {
	encoded := hex.EncodeToString(raw)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

func (m *Manager) activateCandidate(ctx context.Context, c *domain.FreesubBackupConnection, candidate Candidate) error {
	if err := os.MkdirAll(m.cfg.StateDir, 0700); err != nil {
		return err
	}
	configPath := filepath.Join(m.cfg.StateDir, "sing-box.json")
	raw := map[string]any{"log": map[string]any{"disabled": true}, "inbounds": []any{map[string]any{"type": "socks", "tag": "agw-freesub-socks", "listen": "127.0.0.1", "listen_port": m.cfg.SocksPort}}, "outbounds": []any{candidate.Config, map[string]any{"type": "direct", "tag": "direct"}}, "route": map[string]any{"final": candidate.Config["tag"]}}
	if raw["route"].(map[string]any)["final"] == nil || raw["route"].(map[string]any)["final"] == "" {
		raw["route"].(map[string]any)["final"] = "freesub-proxy"
		candidate.Config["tag"] = "freesub-proxy"
	}
	data, _ := json.Marshal(raw)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return err
	}
	check := exec.CommandContext(ctx, m.cfg.SingBoxPath, "check", "-c", configPath)
	if out, err := check.CombinedOutput(); err != nil {
		return fmt.Errorf("sing-box check: %w: %s", err, strings.TrimSpace(string(out)))
	}
	logFile, err := os.OpenFile(filepath.Join(m.cfg.StateDir, "sing-box.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(context.Background(), m.cfg.SingBoxPath, "run", "-c", configPath)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return err
	}
	_ = logFile.Close()
	go func() { _ = cmd.Wait() }()
	m.process = cmd
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", m.cfg.SocksPort), 300*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	exit, probeErr := probeViaSOCKS(ctx, m.cfg.SocksPort)
	if probeErr != nil {
		_ = cmd.Process.Kill()
		logFile.Close()
		return probeErr
	}
	c.CandidateID, c.CountryCode, c.Protocol, c.CandidateIP, c.ExitIP = candidate.CandidateID, candidate.Country, candidate.Protocol, candidate.ExitIP, exit
	c.CandidateConfig = mustJSON(candidate.Config)
	c.SocksPort, c.PublicPort = m.cfg.SocksPort, m.cfg.VLESSPort
	c.RuntimePID = int64(cmd.Process.Pid)
	c.Status = domain.FreesubBackupReady
	c.LastCheckedAt = time.Now().UTC()
	c.LastErrorCode = ""
	return nil
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func (m *Manager) processAlive(pid int64) bool {
	if pid <= 0 {
		return false
	}
	if m.process != nil && m.process.Process != nil && m.process.Process.Pid == int(pid) {
		if runtime.GOOS == "windows" {
			return m.process.ProcessState == nil || !m.process.ProcessState.Exited()
		}
		return m.process.Process.Signal(syscall.Signal(0)) == nil
	}
	p, err := os.FindProcess(int(pid))
	if err != nil {
		return false
	}
	if p.Signal(syscall.Signal(0)) != nil {
		return false
	}
	if runtime.GOOS == "linux" {
		executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		return err == nil && filepath.Clean(executable) == filepath.Clean(m.cfg.SingBoxPath)
	}
	return true
}
func (m *Manager) stopPID(pid int64) {
	if m.process != nil && m.process.Process != nil && m.process.Process.Pid == int(pid) {
		_ = m.process.Process.Kill()
		m.process = nil
		return
	}
	if m.processAlive(pid) {
		p, _ := os.FindProcess(int(pid))
		_ = p.Kill()
	}
}

// probeViaSOCKS uses a minimal SOCKS5 client, avoiding a new runtime dependency.
func probeViaSOCKS(ctx context.Context, port int) (string, error) {
	endpoints := []struct{ host, path string }{{"api.ipify.org", "/"}, {"icanhazip.com", "/"}}
	var failures []error
	for _, endpoint := range endpoints {
		value, err := probeEndpointViaSOCKS(ctx, port, endpoint.host, endpoint.path)
		if err == nil {
			return value, nil
		}
		failures = append(failures, err)
	}
	return "", errors.Join(failures...)
}

func probeEndpointViaSOCKS(ctx context.Context, port int, host, path string) (string, error) {
	dialer := func(address string) (net.Conn, error) {
		return net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)
	}
	conn, err := dialer(host + ":443")
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err = conn.Write([]byte{5, 1, 0}); err != nil {
		return "", err
	}
	resp := make([]byte, 2)
	if _, err = io.ReadFull(conn, resp); err != nil || resp[1] != 0 {
		return "", errors.New("socks greeting failed")
	}
	req := []byte{5, 1, 0, 3, byte(len(host))}
	req = append(req, host...)
	req = append(req, 0x01, 0xbb)
	if _, err = conn.Write(req); err != nil {
		return "", err
	}
	head := make([]byte, 4)
	if _, err = io.ReadFull(conn, head); err != nil || head[1] != 0 {
		return "", errors.New("socks connect failed")
	}
	n := 0
	switch head[3] {
	case 1:
		n = 4
	case 3:
		b := make([]byte, 1)
		io.ReadFull(conn, b)
		n = int(b[0])
	case 4:
		n = 16
	default:
		return "", errors.New("socks address failed")
	}
	body := make([]byte, n+2)
	if _, err = io.ReadFull(conn, body); err != nil {
		return "", err
	}
	// Re-use the established tunnel for an HTTPS request.
	tlsConn := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err = tlsConn.HandshakeContext(ctx); err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+path, nil)
	if err != nil {
		return "", err
	}
	request.Close = true
	if err = request.Write(tlsConn); err != nil {
		return "", err
	}
	response, err := http.ReadResponse(bufio.NewReader(tlsConn), request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("probe returned status %d", response.StatusCode)
	}
	all, err := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(all))
	if net.ParseIP(value) == nil {
		return "", errors.New("probe did not return ip")
	}
	return value, nil
}
