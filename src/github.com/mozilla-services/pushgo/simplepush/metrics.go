/* This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at http://mozilla.org/MPL/2.0/. */

package simplepush

import (
	"bytes"
	"math/rand"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/cactus/go-statsd-client/statsd"
)

// cleanMetricPart is a mapping function passed to strings.Map that replaces
// reserved characters in a metric key with hyphens.
func cleanMetricPart(r rune) rune {
	if r >= 'A' && r <= 'F' {
		r += 'a' - 'A'
	}
	if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
		return r
	}
	return '-'
}

type trec struct {
	Count int64
	Avg   float64
}

type timer map[string]trec

type MetricConfig struct {
	Prefix string
	Suffix string
}

type MetricsConfig struct {
	StoreSnapshots bool   `toml:"store_snapshots" env:"store_snapshots"`
	StatsdServer   string `toml:"statsd_server" env:"statsd_server"`
	StatsdName     string `toml:"statsd_name" env:"statsd_name"`
	Counters       MetricConfig
	Timers         MetricConfig
	Gauges         MetricConfig
}

type Statistician interface {
	Snapshot() map[string]interface{}
	IncrementByRate(name string, delta int64, rate float32)
	IncrementBy(name string, delta int64)
	Increment(name string)
	Decrement(name string)
	Timer(name string, duration time.Duration)
	TimerRate(name string, duration time.Duration, rate float32)
	Gauge(name string, value int64)
	GaugeDelta(name string, delta int64)
	Close() error
}

type StatsClient interface {
	Inc(name string, value int64, rate float32) error
	Dec(name string, value int64, rate float32) error
	Timing(name string, delta int64, rate float32) error
	Gauge(name string, value int64, rate float32) error
	GaugeDelta(name string, delta int64, rate float32) error
	Close() error
}

type Metrics struct {
	mu      sync.RWMutex     // Protects the following lazily-initialized fields.
	counter map[string]int64 // Counter snapshots.
	timer   timer            // Timer snapshots.
	gauge   map[string]int64 // Gauge snapshots.

	// Affixes by metric type.
	counterPrefix string
	counterSuffix string
	timerPrefix   string
	timerSuffix   string
	gaugePrefix   string
	gaugeSuffix   string

	app            *Application
	logger         *SimpleLogger
	statsd         StatsClient
	born           time.Time
	storeSnapshots bool
}

func (m *Metrics) ConfigStruct() interface{} {
	return &MetricsConfig{
		StoreSnapshots: true,
		Gauges:         MetricConfig{Suffix: "{{.Host}}"},
	}
}

func (m *Metrics) Init(app *Application, config interface{}) (err error) {
	conf := config.(*MetricsConfig)
	m.setApp(app)

	if conf.StatsdServer != "" {
		name := strings.ToLower(conf.StatsdName)
		if m.statsd, err = statsd.New(conf.StatsdServer, name); err != nil {
			m.logger.Panic("metrics", "Could not init statsd connection",
				LogFields{"error": err.Error()})
			return err
		}
	}

	err = m.setCounterAffixes(conf.Counters.Prefix, conf.Counters.Suffix)
	if err != nil {
		m.logger.Panic("metrics", "Error setting counter name affixes",
			LogFields{"error": err.Error()})
		return err
	}
	err = m.setTimerAffixes(conf.Timers.Prefix, conf.Timers.Suffix)
	if err != nil {
		m.logger.Panic("metrics", "Error setting timer name affixes",
			LogFields{"error": err.Error()})
		return err
	}
	err = m.setGaugeAffixes(conf.Gauges.Prefix, conf.Gauges.Suffix)
	if err != nil {
		m.logger.Panic("metrics", "Error parsing gauge name affixes",
			LogFields{"error": err.Error()})
		return err
	}
	m.born = timeNow()
	m.storeSnapshots = conf.StoreSnapshots

	return nil
}

func (m *Metrics) setApp(app *Application) {
	m.app = app
	m.logger = app.Logger()
}

func (m *Metrics) setCounterAffixes(rawPrefix, rawSuffix string) (err error) {
	m.counterPrefix, err = m.formatAffix("counterPrefix", rawPrefix)
	if err != nil {
		return err
	}
	m.counterSuffix, err = m.formatAffix("counterSuffix", rawSuffix)
	if err != nil {
		return err
	}
	return nil
}

func (m *Metrics) setTimerAffixes(rawPrefix, rawSuffix string) (err error) {
	if m.timerPrefix, err = m.formatAffix("timerPrefix", rawPrefix); err != nil {
		return err
	}
	if m.timerSuffix, err = m.formatAffix("timerSuffix", rawSuffix); err != nil {
		return err
	}
	return nil
}

func (m *Metrics) setGaugeAffixes(rawPrefix, rawSuffix string) (err error) {
	if m.gaugePrefix, err = m.formatAffix("gaugePrefix", rawPrefix); err != nil {
		return err
	}
	if m.gaugeSuffix, err = m.formatAffix("gaugeSuffix", rawSuffix); err != nil {
		return err
	}
	return nil
}

// formatAffix parses a raw affix as a template, interpolating the hostname
// and server version into the resulting string.
func (m *Metrics) formatAffix(name, raw string) (string, error) {
	tmpl, err := template.New(name).Parse(raw)
	if err != nil {
		return "", err
	}
	affix := new(bytes.Buffer)
	host := strings.Map(cleanMetricPart, m.app.Hostname())
	params := struct {
		Host    string
		Version string
	}{host, VERSION}
	if err := tmpl.Execute(affix, params); err != nil {
		return "", err
	}
	cleanAffix := bytes.TrimRight(affix.Bytes(), ".")
	return string(cleanAffix), nil
}

func (m *Metrics) formatCounter(metric string, tags ...string) string {
	return m.formatMetric(metric, m.counterPrefix, m.counterSuffix, tags...)
}

func (m *Metrics) formatTimer(metric string, tags ...string) string {
	return m.formatMetric(metric, m.timerPrefix, m.timerSuffix, tags...)
}

func (m *Metrics) formatGauge(metric string, tags ...string) string {
	return m.formatMetric(metric, m.gaugePrefix, m.gaugeSuffix, tags...)
}

// formatMetric constructs a statsd key from the given metric name, prefix,
// and suffix. Additional tags are prepended to the suffix.
func (m *Metrics) formatMetric(metric string, prefix, suffix string,
	tags ...string) string {

	var parts []string
	if len(prefix) > 0 {
		parts = append(parts, prefix)
	}
	if len(tags) > 0 {
		parts = append(parts, tags...)
	}
	parts = append(parts, metric)
	if len(suffix) > 0 {
		parts = append(parts, suffix)
	}
	return strings.Join(parts, ".")
}

func (m *Metrics) Snapshot() map[string]interface{} {
	if !m.storeSnapshots {
		return nil
	}
	oldMetrics := make(map[string]interface{})
	// copy the old metrics
	m.mu.RLock()
	for k, v := range m.counter {
		oldMetrics[m.formatCounter(k, "counter")] = v
	}
	for k, v := range m.timer {
		oldMetrics[m.formatTimer(k, "avg")] = v.Avg
	}
	for k, v := range m.gauge {
		oldMetrics[m.formatGauge(k, "gauge")] = v
	}
	m.mu.RUnlock()
	age := timeNow().Unix() - m.born.Unix()
	oldMetrics[m.formatMetric("server.age", "", "")] = age
	return oldMetrics
}

func (m *Metrics) IncrementByRate(metric string, count int64, rate float32) {
	m.storeCounter(metric, count, rate)
	if m.statsd == nil {
		return
	}
	if count >= 0 {
		m.statsd.Inc(m.formatCounter(metric), count, rate)
		return
	}
	m.statsd.Dec(m.formatCounter(metric), -count, rate)
}

func (m *Metrics) IncrementBy(metric string, count int64) {
	m.IncrementByRate(metric, count, 1.0)
}

func (m *Metrics) Increment(metric string) {
	m.IncrementBy(metric, 1)
}

func (m *Metrics) Decrement(metric string) {
	m.IncrementBy(metric, -1)
}

func (m *Metrics) storeCounter(metric string, count int64, rate float32) {
	if !m.storeSnapshots {
		return
	}
	delta := sampleCount(count, rate)
	if delta == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counter == nil {
		m.counter = make(map[string]int64)
	}
	m.counter[metric] += delta
}

func (m *Metrics) resetCounter() (c map[string]int64) {
	m.mu.Lock()
	c, m.counter = m.counter, nil
	m.mu.Unlock()
	return
}

func (m *Metrics) TimerRate(metric string, duration time.Duration, rate float32) {
	// statsd supports millisecond granularity.
	value := int64(duration / time.Millisecond)
	m.storeTimer(metric, value, rate)
	if m.statsd != nil {
		m.statsd.Timing(m.formatTimer(metric), value, rate)
	}
}

func (m *Metrics) Timer(metric string, duration time.Duration) {
	m.TimerRate(metric, duration, 1.0)
}

func (m *Metrics) storeTimer(metric string, value int64, rate float32) {
	if !m.storeSnapshots {
		return
	}
	count := sampleCount(1, rate)
	if count == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timer == nil {
		m.timer = make(timer)
	}
	if t, ok := m.timer[metric]; ok {
		// calculate running average
		t.Count = t.Count + count
		t.Avg = (t.Avg*float64(t.Count-count) + float64(value*count)) / float64(t.Count)
		m.timer[metric] = t
		return
	}
	m.timer[metric] = trec{
		Count: count,
		Avg:   float64(value),
	}
}

func (m *Metrics) resetTimer() (t timer) {
	m.mu.Lock()
	t, m.timer = m.timer, nil
	m.mu.Unlock()
	return
}

func (m *Metrics) Gauge(metric string, value int64) {
	m.storeGauge(metric, value, false)
	if m.statsd == nil {
		return
	}
	if value >= 0 {
		m.statsd.Gauge(m.formatGauge(metric), value, 1.0)
		return
	}
	// Gauges cannot be set to negative values; sign prefixes indicate deltas.
	if err := m.statsd.Gauge(m.formatGauge(metric), 0, 1.0); err != nil {
		return
	}
	m.statsd.GaugeDelta(m.formatGauge(metric), value, 1.0)
}

func (m *Metrics) GaugeDelta(metric string, delta int64) {
	m.storeGauge(metric, delta, true)
	if m.statsd != nil {
		m.statsd.GaugeDelta(m.formatGauge(metric), delta, 1.0)
	}
}

func (m *Metrics) storeGauge(metric string, value int64, delta bool) {
	if !m.storeSnapshots {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gauge == nil {
		m.gauge = make(map[string]int64)
	}
	if delta {
		m.gauge[metric] += value
	} else {
		m.gauge[metric] = value
	}
}

func (m *Metrics) Close() (err error) {
	if m.statsd != nil {
		err = m.statsd.Close()
	}
	return
}

// sampleCount adjusts a value to account for the sample rate, returning 0 if
// the value should not be recorded. The sample rate is clipped to the
// interval [0, 1].
func sampleCount(value int64, rate float32) int64 {
	if rate >= 1 {
		return value
	}
	if rate <= 0 {
		return 0
	}
	if rand.Float32() < rate {
		return 0
	}
	return int64(float32(value) * (1 / rate))
}
