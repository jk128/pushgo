/* This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at http://mozilla.org/MPL/2.0/. */

package simplepush

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/rafrombrc/gomock/gomock"
)

func TestMetricsFormat(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mckLogger := NewMockLogger(mockCtrl)
	mckLogger.EXPECT().ShouldLog(gomock.Any()).Return(true).AnyTimes()
	mckLogger.EXPECT().Log(gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any()).AnyTimes()

	tests := []struct {
		name      string
		hostname  string
		prefix    string
		metric    string
		tags      []string
		asCounter string
		asTimer   string
		asGauge   string
	}{
		{
			name:      "Empty hostname and prefix",
			metric:    "goroutines",
			asCounter: "goroutines",
			asTimer:   "goroutines",
			asGauge:   "goroutines",
		},
		{
			name:      "Hostname; empty prefix",
			hostname:  "localhost",
			metric:    "socket.connect",
			asCounter: "socket.connect",
			asTimer:   "socket.connect",
			asGauge:   "socket.connect.localhost",
		},
		{
			name:      "FQDN; empty prefix",
			hostname:  "test.mozilla.org",
			metric:    "socket.disconnect",
			asCounter: "socket.disconnect",
			asTimer:   "socket.disconnect",
			asGauge:   "socket.disconnect.test-mozilla-org",
		},
		{
			name:      "Empty hostname; prefix",
			prefix:    "pushgo.hello",
			metric:    "updates.rejected",
			asCounter: "pushgo.hello.updates.rejected",
			asTimer:   "pushgo.hello.updates.rejected",
			asGauge:   "pushgo.hello.updates.rejected",
		},
		{
			name:      "IPv4; prefix",
			hostname:  "127.0.0.1",
			prefix:    "pushgo.gcm",
			metric:    "updates.received",
			asCounter: "pushgo.gcm.updates.received",
			asTimer:   "pushgo.gcm.updates.received",
			asGauge:   "pushgo.gcm.updates.received.127-0-0-1",
		},
		{
			name:      "IPv6; tags",
			hostname:  "::1",
			metric:    "ping.success",
			tags:      []string{"stats", "counter"},
			asCounter: "stats.counter.ping.success",
			asTimer:   "stats.counter.ping.success",
			asGauge:   "stats.counter.ping.success.--1",
		},
		{
			name:      "FQDN; prefix; tags",
			hostname:  "test.mozilla.org",
			prefix:    "pushgo.simplepush",
			metric:    "updates.routed.outgoing",
			tags:      []string{"counter"},
			asCounter: "pushgo.simplepush.counter.updates.routed.outgoing",
			asTimer:   "pushgo.simplepush.counter.updates.routed.outgoing",
			asGauge:   "pushgo.simplepush.counter.updates.routed.outgoing.test-mozilla-org",
		},
	}
	for _, test := range tests {
		app := NewApplication()
		app.hostname = test.hostname
		app.SetLogger(mckLogger)
		m := new(Metrics)
		m.setApp(app)
		// Test the default affixes.
		conf := m.ConfigStruct().(*MetricsConfig)
		err := m.setCounterAffixes(test.prefix, conf.Counters.Suffix)
		if err != nil {
			t.Errorf("On test %s, error setting counter name affixes: %s",
				test.name, err)
			continue
		}
		if err = m.setTimerAffixes(test.prefix, conf.Timers.Suffix); err != nil {
			t.Errorf("On test %s, error setting timer name affixes: %s",
				test.name, err)
			continue
		}
		if err = m.setGaugeAffixes(test.prefix, conf.Gauges.Suffix); err != nil {
			t.Errorf("On test %s, error setting gauge name affixes: %s",
				test.name, err)
			continue
		}
		app.SetMetrics(m)
		asCounter := m.formatCounter(test.metric, test.tags...)
		if asCounter != test.asCounter {
			t.Errorf("On test %s, wrong counter stat name: got %q; want %q",
				test.name, asCounter, test.asCounter)
		}
		asTimer := m.formatTimer(test.metric, test.tags...)
		if asTimer != test.asTimer {
			t.Errorf("On test %s, wrong timer stat name: got %q; want %q",
				test.name, asTimer, test.asTimer)
		}
		asGauge := m.formatGauge(test.metric, test.tags...)
		if asGauge != test.asGauge {
			t.Errorf("On test %s, wrong gauge stat name: got %q; want %q",
				test.name, asGauge, test.asGauge)
		}
	}
}

func TestMetricsPrefix(t *testing.T) {
	useMockFuncs()
	defer useStdFuncs()

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	app := NewApplication()
	app.hostname = "example.com"
	mckClient := NewMockStatsClient(mockCtrl)

	tests := []struct {
		name       string
		prefix     string
		key        string
		tagCounter string
		tagTimer   string
		tagGauge   string
		tagAge     string
	}{
		{"Prefix", "example", "example.test", "example.counter.test", "example.avg.test", "example.gauge.test", "server.age"},
		{"Prefix containing dot", "push.example", "push.example.test", "push.example.counter.test", "push.example.avg.test", "push.example.gauge.test", "server.age"},
		{"Prefix containing trailing dot", "push.", "push.test", "push.counter.test", "push.avg.test", "push.gauge.test", "server.age"},
		{"Empty prefix", "", "test", "counter.test", "avg.test", "gauge.test", "server.age"},
	}
	for _, test := range tests {
		m := new(Metrics)
		m.setApp(app)
		m.born = timeNow().Add(-5 * time.Minute)
		m.statsd = mckClient
		m.storeSnapshots = true
		gomock.InOrder(
			mckClient.EXPECT().Inc(test.key, int64(1), float32(1.0)),
			mckClient.EXPECT().Timing(test.key, int64(2000), float32(1.0)),
			mckClient.EXPECT().Gauge(test.key, int64(3), float32(1.0)),
		)
		err := m.setCounterAffixes(test.prefix, "")
		if err != nil {
			t.Errorf("On test %s, error setting counter name affixes: %s",
				test.name, err)
			continue
		}
		if err = m.setTimerAffixes(test.prefix, ""); err != nil {
			t.Errorf("On test %s, error setting timer name affixes: %s",
				test.name, err)
			continue
		}
		if err = m.setGaugeAffixes(test.prefix, ""); err != nil {
			t.Errorf("On test %s, error setting gauge name affixes: %s",
				test.name, err)
			continue
		}
		m.Increment("test")
		m.Timer("test", 2*time.Second)
		m.Gauge("test", 3)
		expected := map[string]interface{}{
			test.tagCounter: int64(1),
			test.tagTimer:   float64(2000),
			test.tagGauge:   int64(3),
			test.tagAge:     int64(5 * time.Minute / time.Second),
		}
		actual := m.Snapshot()
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("On test %s, got %#v; want %#v", test.name, actual, expected)
		}
		mckClient.EXPECT().Close()
		m.Close()
	}
}

func TestMetricsIncrement(t *testing.T) {
	useMockFuncs()
	defer useStdFuncs()

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mckClient := NewMockStatsClient(mockCtrl)

	m := new(Metrics)
	m.born = timeNow().Add(-10 * time.Second)
	m.statsd = mckClient
	m.storeSnapshots = true

	gomock.InOrder(
		mckClient.EXPECT().Inc("a", int64(1), float32(1.0)).Times(3),
		mckClient.EXPECT().Inc("b", int64(3), float32(1.0)).Times(3),
		mckClient.EXPECT().Inc("c", int64(0), float32(1.0)),
	)
	for i := 0; i < 6; i++ {
		if i < 3 {
			m.Increment("a")
		} else {
			m.IncrementBy("b", 3)
		}
	}
	m.IncrementBy("c", 0)
	expected := map[string]interface{}{
		"counter.a":  int64(3),
		"counter.b":  int64(9),
		"server.age": int64(10),
	}
	actual := m.Snapshot()
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("Wrong counter values: got %#v; want %#v",
			actual, expected)
	}

	mckClient.EXPECT().Close()
	m.Close()
}

func TestMetricsDecrement(t *testing.T) {
	useMockFuncs()
	defer useStdFuncs()

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mckClient := NewMockStatsClient(mockCtrl)

	m := new(Metrics)
	m.born = timeNow().Add(-10 * time.Second)
	m.statsd = mckClient
	m.storeSnapshots = true

	gomock.InOrder(
		mckClient.EXPECT().Dec("a", int64(1), float32(1.0)).Times(3),
		mckClient.EXPECT().Dec("b", int64(4), float32(1.0)).Times(3),
	)
	for i := 0; i < 6; i++ {
		if i < 3 {
			m.Decrement("a")
		} else {
			m.IncrementBy("b", -4)
		}
	}
	expected := map[string]interface{}{
		"counter.a":  int64(-3),
		"counter.b":  int64(-12),
		"server.age": int64(10),
	}
	actual := m.Snapshot()
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("Wrong counter values: got %#v; want %#v",
			actual, expected)
	}

	mckClient.EXPECT().Close()
	m.Close()
}

func TestMetricsInvalidSampleRate(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mckClient := NewMockStatsClient(mockCtrl)

	m := new(Metrics)
	m.statsd = mckClient
	m.storeSnapshots = true

	gomock.InOrder(
		mckClient.EXPECT().Inc("a", int64(2), float32(1.25)),
		mckClient.EXPECT().Dec("a", int64(4), float32(-0.1)),
		mckClient.EXPECT().Timing("b", int64(25), float32(2.0)),
		mckClient.EXPECT().Timing("b", int64(-500), float32(-4.0)),
	)
	rand.Seed(1)
	m.IncrementByRate("a", 2, 1.25)
	m.IncrementByRate("a", -4, -0.1)
	m.TimerRate("b", 25*time.Millisecond, 2.0)
	m.TimerRate("b", -500*time.Millisecond, -4.0)

	counter := m.resetCounter()
	if actual := counter["a"]; actual != 2 {
		t.Errorf("Wrong counter value: got %d; want 2")
	}
	timer := m.resetTimer()
	if actual := timer["b"]; actual.Count != 1 || actual.Avg != 25 {
		t.Errorf("Wrong timing value: got (%d, %f); want (1, 2000)",
			actual.Count, actual.Avg)
	}
}

func TestMetricsGaugeNegative(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mckClient := NewMockStatsClient(mockCtrl)

	m := new(Metrics)
	m.statsd = mckClient
	m.storeSnapshots = true

	// Should set negative numbers by setting the gauge to 0, followed by a
	// negative delta. This admits a race if the delta packet is received first.
	gomock.InOrder(
		mckClient.EXPECT().Gauge("a", int64(0), float32(1.0)),
		mckClient.EXPECT().GaugeDelta("a", int64(-5), float32(1.0)),
	)
	m.Gauge("a", -5)
	snapshot := m.Snapshot()
	if actual, _ := snapshot["gauge.a"].(int64); actual != -5 {
		t.Errorf("Wrong gauge value after successful client call: got %d; want -5",
			actual)
	}

	// Should not send a delta packet if setting the gauge to 0 fails.
	mckClient.EXPECT().Gauge("b", int64(0), float32(1.0)).Return(
		errors.New("oops"))
	m.Gauge("b", -10)
	snapshot = m.Snapshot()
	if actual, _ := snapshot["gauge.b"].(int64); actual != -10 {
		t.Errorf("Wrong gauge value after failed client call: got %d; want -10",
			actual)
	}

	mckClient.EXPECT().Close()
	m.Close()
}

func TestMetricsGauge(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mckClient := NewMockStatsClient(mockCtrl)

	m := new(Metrics)
	m.statsd = mckClient
	m.storeSnapshots = true

	gomock.InOrder(
		mckClient.EXPECT().Gauge("a", int64(1), float32(1.0)),
		mckClient.EXPECT().Gauge("a", int64(0), float32(1.0)),
		mckClient.EXPECT().Gauge("a", int64(8), float32(1.0)),
	)
	for _, value := range []int64{1, 0, 8} {
		m.Gauge("a", value)
	}
	if actual, _ := m.Snapshot()["gauge.a"].(int64); actual != 8 {
		t.Errorf("Wrong gauge value: got %d; want 8", actual)
	}

	mckClient.EXPECT().Close()
	m.Close()
}

func TestMetricsGaugeDelta(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mckClient := NewMockStatsClient(mockCtrl)

	m := new(Metrics)
	m.statsd = mckClient
	m.storeSnapshots = true

	gomock.InOrder(
		mckClient.EXPECT().GaugeDelta("a", int64(6), float32(1.0)),
		mckClient.EXPECT().GaugeDelta("a", int64(3), float32(1.0)),
		mckClient.EXPECT().GaugeDelta("a", int64(-12), float32(1.0)),
	)
	for _, delta := range []int64{6, 3, -12} {
		m.GaugeDelta("a", delta)
	}
	if actual, _ := m.Snapshot()["gauge.a"].(int64); actual != -3 {
		t.Errorf("Wrong gauge value: got %d; want -3", actual)
	}

	mckClient.EXPECT().Close()
	m.Close()
}

func TestMetricsSampleRate(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mckClient := NewMockStatsClient(mockCtrl)

	m := new(Metrics)
	m.statsd = mckClient
	m.storeSnapshots = true

	gomock.InOrder(
		mckClient.EXPECT().Timing("a", int64(3000), float32(1.0)).Times(4),
		mckClient.EXPECT().Timing("a", int64(500), float32(1.0)).Times(5),
		mckClient.EXPECT().Timing("a", int64(25), float32(1.0)).Times(10),
		mckClient.EXPECT().Inc("a", int64(10), float32(1.0)).Times(8),
		mckClient.EXPECT().Dec("a", int64(5), float32(1.0)).Times(2),
	)
	tests := []struct {
		times      int
		timeDelta  time.Duration
		countDelta int64
	}{
		{4, 3 * time.Second, 0},
		{5, 500 * time.Millisecond, 0},
		{10, 25500 * time.Microsecond, 0}, // Microseconds should be truncated.
		{8, 0, 10},
		{2, 0, -5},
	}
	for _, test := range tests {
		for i := 0; i < test.times; i++ {
			if test.timeDelta > 0 {
				m.Timer("a", test.timeDelta)
			} else {
				m.IncrementBy("a", test.countDelta)
			}
		}
	}

	// Ensure sampled counters and timers produce equivalent values.
	prevCounter := m.resetCounter()
	prevTimer := m.resetTimer()
	gomock.InOrder(
		mckClient.EXPECT().Timing("a", int64(3000), float32(0.25)),
		mckClient.EXPECT().Timing("a", int64(500), float32(0.2)),
		mckClient.EXPECT().Timing("a", int64(25), float32(0.1)),
		mckClient.EXPECT().Inc("a", int64(10), float32(0.125)),
		mckClient.EXPECT().Dec("a", int64(5), float32(0.5)).Times(2),
	)
	rand.Seed(1)
	m.TimerRate("a", 3*time.Second, 0.25)
	m.TimerRate("a", 500*time.Millisecond, 0.2)
	m.TimerRate("a", 25500*time.Microsecond, 0.1)
	m.IncrementByRate("a", 10, 0.125)
	m.IncrementByRate("a", -5, 0.5) // Not recorded.
	m.IncrementByRate("a", -5, 0.5)
	if counter := m.resetCounter(); !reflect.DeepEqual(counter, prevCounter) {
		t.Errorf("Wrong sampled counter stats: got %#v; want %#v",
			counter, prevCounter)
	}
	if timer := m.resetTimer(); !reflect.DeepEqual(timer, prevTimer) {
		t.Errorf("Wrong sampled timing stats: got %#v; want %#v",
			timer, prevTimer)
	}
	mckClient.EXPECT().Close()
	m.Close()
}
