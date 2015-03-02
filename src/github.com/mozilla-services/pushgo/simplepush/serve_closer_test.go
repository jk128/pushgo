//+build ignore

/* This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at http://mozilla.org/MPL/2.0/. */

package simplepush

import (
	"bufio"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestServeCloserClose(t *testing.T) {
	pipe := newPipeListener()
	defer pipe.Close()

	var wg sync.WaitGroup // Synchronizes client and closer.
	wg.Add(1)
	closeChan := make(chan bool) // Synchronizes closer and second request.
	timeout := closeAfter(2 * time.Second)

	srv := newServeCloser(&http.Server{
		Handler: http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			resp.WriteHeader(http.StatusAccepted)
			io.WriteString(resp, "hello")
		}),
	})
	go func() {
		defer wg.Done()
		requestURL := &url.URL{Scheme: "http", Host: "example.com"}
		client := &http.Client{
			Transport: &http.Transport{Dial: pipe.Dial},
		}
		// Initial request should succeed.
		a := &http.Request{Method: "GET", URL: requestURL, Close: true}
		resp, err := client.Do(a)
		if err != nil {
			t.Errorf("Error sending request to active server: %s", err)
			return
		}
		drainReader(resp.Body)
		if resp.StatusCode != http.StatusAccepted {
			t.Errorf("Wrong response status from active server: got %d; want %d",
				resp.StatusCode, http.StatusAccepted)
		}
		select {
		case closeChan <- true:
		case <-timeout:
			t.Errorf("Timed out waiting for server to close")
		}
		<-closeChan // Wait for the server to shut down.
		// Second request should fail.
		b := &http.Request{Method: "GET", URL: requestURL, Close: true}
		resp, err = client.Do(b)
		if err != nil {
			return
		}
		drainReader(resp.Body)
		t.Errorf("Got response status %d from closed server; want error",
			resp.StatusCode)
	}()
	go srv.Serve(pipe)
	select {
	case <-closeChan:
	case <-timeout:
		t.Errorf("Timed out waiting for client to make request to active server")
	}
	srv.Close()
	close(closeChan)
	wg.Wait()
}

func TestServeCloserNotify(t *testing.T) {
	pipe := newPipeListener()
	l := &LimitListener{Listener: pipe, MaxConns: 1}
	defer l.Close()

	var wg sync.WaitGroup // Synchronizes handler and client.
	wg.Add(2)
	reqChan := make(chan bool) // Synchronizes handler and closer.
	timeout := closeAfter(2 * time.Second)

	srv := newServeCloser(&http.Server{
		ErrorLog: log.New(ioutil.Discard, "", 0),
		Handler: http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			defer wg.Done()
			select {
			case reqChan <- true:
			case <-timeout:
				t.Errorf("Timed out waiting for client to send request")
				return
			}
			<-reqChan // Wait for the server to shut down.
			select {
			case <-res.(http.CloseNotifier).CloseNotify():
				return
			case <-timeout:
				t.Errorf("Timed out waiting for server to close the connection")
			}
			io.WriteString(res, "should not respond")
		}),
	})

	go func() {
		defer wg.Done()
		client := &http.Client{
			Transport: &http.Transport{Dial: pipe.Dial},
		}
		req := &http.Request{
			Method: "GET",
			URL:    &url.URL{Scheme: "http", Host: "example.com"},
			Close:  true,
		}
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		drainReader(resp.Body)
		t.Errorf("Got response status %d; want error", resp.StatusCode)
	}()

	go srv.Serve(l)
	select {
	case <-reqChan:
	case <-timeout:
		t.Errorf("Timed out waiting for server to receive request")
	}
	srv.Close()
	close(reqChan)
	wg.Wait()
}

func TestServeCloserPanic(t *testing.T) {
	pipe := newPipeListener()
	defer pipe.Close()

	var statesLock sync.RWMutex
	connStates := make(map[net.Conn][]http.ConnState)

	var wg sync.WaitGroup // Synchronizes handler and clients.
	requests := 3         // Number of concurrent requests to make.
	wg.Add(requests * 2)

	srv := newServeCloser(&http.Server{
		ErrorLog: log.New(ioutil.Discard, "", 0),
		ConnState: func(c net.Conn, state http.ConnState) {
			statesLock.Lock()
			connStates[c] = append(connStates[c], state)
			statesLock.Unlock()
			if isTerminalState(state) {
				wg.Done()
			}
		},
		Handler: http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			resp.WriteHeader(http.StatusBadRequest)
			io.WriteString(resp, "about to crash")
			resp.(http.Flusher).Flush() // Flush the response before panicking.
			panic("omg, everything is exploding")
		}),
	})
	defer srv.Close()

	client := &http.Client{
		Transport: &http.Transport{Dial: pipe.Dial},
		Timeout:   2 * time.Second,
	}
	doRequest := func(i int) {
		defer wg.Done()
		req := &http.Request{
			Method: "GET",
			URL:    &url.URL{Scheme: "http", Host: "example.com"},
			Close:  true,
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("Error sending request %d: %s", i, err)
			return
		}
		drainReader(resp.Body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Wrong response status for request %d: got %d; want %d",
				i, resp.StatusCode, http.StatusBadRequest)
		}
	}
	for i := 0; i < requests; i++ {
		go doRequest(i)
	}

	go srv.Serve(pipe)
	wg.Wait() // Wait for the connection to terminate.

	statesLock.RLock()
	defer statesLock.RUnlock()
	if len(connStates) != requests {
		t.Errorf("Wrong connection count: got %d; want %d",
			len(connStates), requests)
	}
	for c, states := range connStates {
		if srv.hasConn(c) {
			t.Errorf("Connection %#v not removed from server after panic", c)
		}
		expectedStates := []http.ConnState{http.StateNew, http.StateActive,
			http.StateClosed}
		if !reflect.DeepEqual(states, expectedStates) {
			t.Errorf("Wrong states for connection %#v: got %#v; want %#v",
				c, states, expectedStates)
		}
	}
}

func TestServeCloserHijack(t *testing.T) {
	pipe := newPipeListener()
	l := &LimitListener{Listener: pipe, MaxConns: 1}
	defer l.Close()

	var wg sync.WaitGroup // Synchronizes handler and client.
	wg.Add(2)
	reqChan := make(chan bool) // Synchronizes handler and closer.
	timeout := closeAfter(2 * time.Second)

	srv := newServeCloser(&http.Server{
		ErrorLog: log.New(ioutil.Discard, "", 0), // Squelch errTooBusy log entries.
		Handler: http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			defer wg.Done()
			c, rw, err := res.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("Error hijacking server connection: %s", err)
				return
			}
			defer c.Close()
			select {
			case reqChan <- true:
				// Signal the server to shut down. Since hijacked connections are removed
				// from the map, c should not be closed.
			case <-timeout:
				t.Errorf("Timed out waiting for client to send request")
			}
			<-reqChan // Wait for the server to shut down.
			expected := "Hello, world\n"
			rw.WriteString(expected)
			if err := rw.Flush(); err != nil {
				t.Errorf("Error writing to hijacked server connection: %s", err)
				return
			}
			actual, err := rw.ReadString('\n')
			if err != nil {
				t.Errorf("Error reading from hijacked server connection: %s", err)
				return
			}
			if actual != expected {
				t.Errorf("Client echoed wrong string: got %q; want %q",
					actual, expected)
				return
			}
		}),
	})

	go func() {
		defer wg.Done()
		c, err := pipe.Dial("", "")
		if err != nil {
			t.Errorf("Error dialing server: %s", err)
			return
		}
		defer c.Close()
		rw := bufio.NewReadWriter(bufio.NewReader(c), bufio.NewWriter(c))
		rw.WriteString("GET / HTTP/1.1\r\n")
		rw.WriteString("Host: example.com\r\n")
		rw.WriteString("\r\n")
		if err := rw.Flush(); err != nil {
			t.Errorf("Error writing client request: %s", err)
			return
		}
		echo, err := rw.ReadString('\n')
		if err != nil {
			t.Errorf("Error reading from client connection: %s", err)
			return
		}
		rw.WriteString(echo)
		if err := rw.Flush(); err != nil {
			t.Errorf("Error writing to client connection: %s", err)
			return
		}
	}()

	go srv.Serve(l)
	select {
	case <-reqChan:
	case <-timeout:
		t.Errorf("Timed out waiting for server to hijack the connection")
	}
	srv.Close()
	close(reqChan)
	wg.Wait()
}
