package deamon

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openhue/openhue-go"
	"golang.org/x/sys/unix"

	"github.com/PTRK-DE/openhued/deamon/config"
)

type Daemon struct {
	home                *openhue.Home
	cfg                 *config.Config
	mu                  sync.Mutex
	light               *openhue.GroupedLightGet
	socket              string
	lastKnownBrightness float32
	debounceDelay       time.Duration
	debounceTimer       *time.Timer
	pendingBrightness   *float32
}

// NewDaemon connects to the bridge and loads initial state.
func NewDaemon(cfg *config.Config, socketPath string) (*Daemon, error) {
	home, err := openhue.NewHome(openhue.LoadConfNoError())
	if err != nil {
		// no need to call LoadConf() again here
		return nil, fmt.Errorf("connect Hue bridge: %w", err)
	}

	gl, err := home.GetGroupedLightById(cfg.LightID)
	if err != nil {
		return nil, fmt.Errorf("get grouped light: %w", err)
	}

	lastBrightness := float32(100)
	if gl != nil && gl.Dimming != nil && gl.Dimming.Brightness != nil && *gl.Dimming.Brightness > 0 {
		lastBrightness = *gl.Dimming.Brightness
	}

	d := &Daemon{
		home:                home,
		cfg:                 cfg,
		light:               gl,
		socket:              socketPath,
		lastKnownBrightness: lastBrightness,
		debounceDelay:       time.Duration(cfg.CommandDebounceMs) * time.Millisecond,
	}

	// Start the event stream to receive updates
	go d.startEventStream()

	return d, nil
}

func runtimeDir() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = "/tmp"
	}
	return base
}

func DefaultSocketPath() string {
	uid := os.Getuid()
	return filepath.Join(runtimeDir(), fmt.Sprintf("openhued-%d.sock", uid))
}

// Run starts the Unix socket server and blocks.
func (d *Daemon) Run() error {
	// remove stale socket
	_ = os.Remove(d.socket)

	l, err := net.Listen("unix", d.socket)
	if err != nil {
		return fmt.Errorf("listen unix socket: %w", err)
	}
	defer l.Close()

	if err := os.Chmod(d.socket, 0o600); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}

	fmt.Println("openhued: listening on", d.socket)

	for {
		conn, err := l.Accept()
		if err != nil {
			// don't crash the daemon on a transient error
			fmt.Fprintf(os.Stderr, "accept error: %v\n", err)
			continue
		}
		go d.handleConn(conn)
	}
}

func (d *Daemon) currentBrightnessPercent() int {
	if d.light == nil {
		return 0
	}

	if d.light.Dimming != nil && d.light.Dimming.Brightness != nil {
		if v := float32(*d.light.Dimming.Brightness); v > 0 {
			d.rememberBrightnessLocked(v)
		}
	}

	if !d.light.IsOn() {
		return 0
	}

	if d.light.Dimming != nil && d.light.Dimming.Brightness != nil {
		if v := float32(*d.light.Dimming.Brightness); v > 0 {
			return int(v)
		}
	}

	if d.lastKnownBrightness > 0 {
		return int(d.lastKnownBrightness)
	}

	return 100
}

func (d *Daemon) rememberBrightnessLocked(v float32) {
	if v > 0 {
		d.lastKnownBrightness = v
	}
}

func (d *Daemon) ensureBrightnessCachedLocked() {
	if d.light == nil || !d.light.IsOn() {
		return
	}
	if d.lastKnownBrightness <= 0 {
		return
	}
	if d.light.Dimming == nil {
		d.light.Dimming = &openhue.Dimming{}
	}
	if d.light.Dimming.Brightness == nil || *d.light.Dimming.Brightness <= 0 {
		nb := openhue.Brightness(d.lastKnownBrightness)
		d.light.Dimming.Brightness = &nb
	}
}

func (d *Daemon) printBrightness() {
	if d.cfg != nil && !d.cfg.StreamBrightness {
		return
	}
	fmt.Printf("%d%%\n", d.currentBrightnessPercent())
}

func (d *Daemon) handleConn(c net.Conn) {
	defer c.Close()

	reader := bufio.NewReader(c)
	line, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "read command: %v\n", err)
		return
	}
	cmd := strings.TrimSpace(line)

	var resp string
	switch cmd {
	case "toggle":
		resp, err = d.toggle()
	case "up":
		resp, err = d.adjustBrightness("up")
	case "down":
		resp, err = d.adjustBrightness("down")
	case "status":
		resp, err = d.getBrightnessStatus()
	default:
		resp = fmt.Sprintf("error: unknown command %q\n", cmd)
	}

	if err != nil {
		resp = fmt.Sprintf("error: %v\n", err)
	}
	c.Write([]byte(resp))
}

func (d *Daemon) getBrightnessStatus() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	return fmt.Sprintf("%d%%\n", d.currentBrightnessPercent()), nil
}

// toggle uses cached state and optimistically updates local state.
func (d *Daemon) toggle() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.debounceTimer != nil {
		d.debounceTimer.Stop()
	}
	d.pendingBrightness = nil

	if d.light.IsOn() && d.light.Dimming != nil && d.light.Dimming.Brightness != nil {
		d.rememberBrightnessLocked(float32(*d.light.Dimming.Brightness))
	}

	targetOn := d.light.Toggle()
	body := openhue.GroupedLightPut{On: targetOn}
	restoringBrightness := targetOn != nil && targetOn.On != nil && *targetOn.On && d.lastKnownBrightness > 0

	if restoringBrightness {
		nb := openhue.Brightness(d.lastKnownBrightness)
		body.Dimming = &openhue.Dimming{Brightness: &nb}
	}

	// Send toggle command to bridge
	if err := d.home.UpdateGroupedLight(*d.light.Id, body); err != nil {
		return "", fmt.Errorf("toggle light: %w", err)
	}

	// Optimistically update local state
	if d.light.On == nil {
		d.light.On = &openhue.On{}
	}
	d.light.On.On = targetOn.On
	if restoringBrightness {
		if d.light.Dimming == nil {
			d.light.Dimming = &openhue.Dimming{}
		}
		nb := openhue.Brightness(d.lastKnownBrightness)
		d.light.Dimming.Brightness = &nb
	}

	state := "off"
	if d.light.IsOn() {
		state = "on"
		d.ensureBrightnessCachedLocked()
	}

	d.printBrightness()

	return fmt.Sprintf("ok: light toggled, now %s\n", state), nil
}

func (d *Daemon) adjustBrightness(direction string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	cur := d.lastKnownBrightness
	if cur <= 0 {
		if d.light.Dimming != nil && d.light.Dimming.Brightness != nil && *d.light.Dimming.Brightness > 0 {
			cur = *d.light.Dimming.Brightness
			d.rememberBrightnessLocked(cur)
		} else {
			cur = float32(100)
		}
	}

	if !d.light.IsOn() {
		return "", fmt.Errorf("brightness adjustments are only permitted when the light is on")
	}

	switch direction {
	case "up":
		cur += float32(d.cfg.BrightnessIncrement)
	case "down":
		cur -= float32(d.cfg.BrightnessIncrement)
	default:
		return "", fmt.Errorf("invalid direction: %q", direction)
	}

	if cur < 0 {
		cur = 0
	}
	if cur > 100 {
		cur = 100
	}

	nb := openhue.Brightness(cur)
	if d.light.Dimming == nil {
		d.light.Dimming = &openhue.Dimming{}
	}
	d.light.Dimming.Brightness = &nb
	d.rememberBrightnessLocked(cur)

	d.queueBrightnessUpdateLocked(cur)
	d.printBrightness()
	return fmt.Sprintf("ok: brightness %s to %d%%\n", direction, int(cur+0.5)), nil
}

func (d *Daemon) queueBrightnessUpdateLocked(target float32) {
	d.pendingBrightness = &target
	if d.debounceDelay <= 0 {
		go d.flushPendingBrightness()
		return
	}
	if d.debounceTimer == nil {
		d.debounceTimer = time.AfterFunc(d.debounceDelay, d.flushPendingBrightness)
		return
	}
	d.debounceTimer.Reset(d.debounceDelay)
}

func (d *Daemon) flushPendingBrightness() {
	d.mu.Lock()
	if d.pendingBrightness == nil || d.light == nil || d.light.Id == nil {
		d.mu.Unlock()
		return
	}
	target := *d.pendingBrightness
	d.pendingBrightness = nil
	lightID := *d.light.Id
	d.mu.Unlock()

	nb := openhue.Brightness(target)
	if err := d.home.UpdateGroupedLight(lightID, openhue.GroupedLightPut{
		Dimming: &openhue.Dimming{Brightness: &nb},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "adjust brightness (debounced): %v\n", err)
	}
}

// Optional: keep your lock-file helper if you still want mutual exclusion
func WithFileLock(path string, fn func() error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("lock open: %w", err)
	}
	defer f.Close()

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock acquire: %w", err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)

	return fn()
}

func (d *Daemon) startEventStream() {
	bridgeIP, apiKey := openhue.LoadConfNoError()
	url := fmt.Sprintf("https://%s/eventstream/clip/v2", bridgeIP)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	var lastEventID string

	for {
		nextID, err := d.consumeEventStream(client, url, apiKey, lastEventID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "event stream error: %v\n", err)
		}
		lastEventID = nextID

		// Backoff before reconnect
		time.Sleep(5 * time.Second)
	}
}

func (d *Daemon) consumeEventStream(client *http.Client, url, apiKey, lastEventID string) (string, error) {
	currentLast := lastEventID

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return currentLast, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("hue-application-key", apiKey)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}

	resp, err := client.Do(req)
	if err != nil {
		return currentLast, fmt.Errorf("connecting to event stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return currentLast, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	fmt.Println("Connected to Hue event stream")

	d.mu.Lock()
	d.printBrightness()
	d.mu.Unlock()

	reader := bufio.NewReader(resp.Body)
	var dataLines []string

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return currentLast, fmt.Errorf("read error: %w", err)
		}

		line = strings.TrimRight(line, "\r\n")

		// SSE "id:" line – remember lastEventID for potential resume
		if strings.HasPrefix(line, "id:") {
			currentLast = strings.TrimSpace(line[3:])
			continue
		}

		// Empty line = event boundary
		if line == "" {
			if len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")

				var events []map[string]interface{}
				if err := json.Unmarshal([]byte(data), &events); err != nil {
					fmt.Printf("JSON parse error: %v\n", err)
				} else {
					d.handleEventBatch(events)
				}

				dataLines = dataLines[:0]
			}
			continue
		}

		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(line[5:]))
		}
	}
}

// handleEventBatch processes one SSE event payload (array of events).
func (d *Daemon) handleEventBatch(events []map[string]interface{}) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.light == nil || d.light.Id == nil {
		return
	}
	targetID := *d.light.Id

	for _, ev := range events {
		rawData, ok := ev["data"].([]interface{})
		if !ok {
			continue
		}

		for _, item := range rawData {
			res, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			rtype, _ := res["type"].(string)
			id, _ := res["id"].(string)

			// We only care about the grouped_light we control
			if rtype != "grouped_light" || id != targetID {
				continue
			}

			// dimming.brightness
			if dimming, ok := res["dimming"].(map[string]interface{}); ok {
				if b, ok := dimming["brightness"].(float64); ok {
					if d.light.Dimming == nil {
						d.light.Dimming = &openhue.Dimming{}
					}
					nb := openhue.Brightness(float32(b))
					d.light.Dimming.Brightness = &nb
					d.rememberBrightnessLocked(float32(b))
				}
			}

			// on: {"on": true/false}
			if onObj, ok := res["on"].(map[string]interface{}); ok {
				if v, ok := onObj["on"].(bool); ok {
					if d.light.On == nil {
						d.light.On = &openhue.On{}
					}
					d.light.On.On = &v
				}
			}
		}
	}
}
