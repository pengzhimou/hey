// Copyright 2014 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package requester provides commands to run load tests and display results.
package requester

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

// Max size of the buffer of result channel.
const maxResult = 1000000
const maxIdleConn = 500

type result struct {
	err           error
	statusCode    int
	offset        time.Duration
	duration      time.Duration
	connDuration  time.Duration // connection setup(DNS lookup + Dial up) duration
	dnsDuration   time.Duration // dns lookup duration
	reqDuration   time.Duration // request "write" duration
	resDuration   time.Duration // response "read" duration
	delayDuration time.Duration // delay between response and request
	contentLength int64
}

type Work struct {
	// Request is the request to be made.
	Request *http.Request

	RequestBody string

	// RequestFunc is a function to generate requests. If it is nil, then
	// Request and RequestData are cloned for each request.
	RequestFunc func() *http.Request

	// N is the total number of requests to make.
	N int

	// C is the concurrency level, the number of concurrent workers to run.
	C int

	// H2 is an option to make HTTP/2 requests
	H2 bool

	// Timeout in seconds.
	Timeout int

	// Qps is the rate limit in queries per second.
	QPS float64

	// DisableCompression is an option to disable compression in response
	DisableCompression bool

	// DisableKeepAlives is an option to prevents re-use of TCP connections between different HTTP requests
	DisableKeepAlives bool

	// DisableRedirects is an option to prevent the following of HTTP redirects
	DisableRedirects bool

	// Output represents the output type. If "csv" is provided, the
	// output will be dumped as a csv stream.
	Output string

	// ProxyAddr is the address of HTTP proxy server in the format on "host:port".
	// Optional.
	ProxyAddr *url.URL

	// Writer is where results will be written. If nil, results are written to stdout.
	Writer io.Writer

	initOnce sync.Once
	results  chan *result
	stopCh   chan struct{}
	start    time.Duration

	report *report

	Certfile string
	Keyfile  string

	RandMark bool
}

func (b *Work) writer() io.Writer {
	if b.Writer == nil {
		return os.Stdout
	}
	return b.Writer
}

// Init initializes internal data-structures
func (b *Work) Init() {
	b.initOnce.Do(
		func() {
			b.results = make(chan *result, min(b.C*1000, maxResult))
			b.stopCh = make(chan struct{}, b.C)
		},
	)
}

// Run makes all the requests, prints the summary. It blocks until
// all work is done.
func (b *Work) Run() {
	b.Init()
	b.start = now()
	b.report = newReport(b.writer(), b.results, b.Output, b.N)
	// Run the reporter first, it polls the result channel until it is closed.
	go func() {
		runReporter(b.report)
	}()
	b.runWorkers()
	b.Finish()
}

func (b *Work) Stop() {
	// Send stop signal so that workers can stop gracefully.
	for i := 0; i < b.C; i++ {
		b.stopCh <- struct{}{}
	}
}

func (b *Work) Finish() {
	close(b.results)
	total := now() - b.start
	// Wait until the reporter is done.
	<-b.report.done
	b.report.finalize(total)
}

func (b *Work) makeRequest(gort, n int, c *http.Client) {
	s := now()
	var size int64
	var code int
	var dnsStart, connStart, resStart, reqStart, delayStart time.Duration
	var dnsDuration, connDuration, resDuration, reqDuration, delayDuration time.Duration
	var req *http.Request
	if b.RequestFunc != nil {
		req = b.RequestFunc()
	} else {
		req = cloneRequest(b.Request, b.RequestBody)
	}
	trace := &httptrace.ClientTrace{
		DNSStart: func(info httptrace.DNSStartInfo) {
			dnsStart = now()
		},
		DNSDone: func(dnsInfo httptrace.DNSDoneInfo) {
			dnsDuration = now() - dnsStart
		},
		GetConn: func(h string) {
			connStart = now()
		},
		GotConn: func(connInfo httptrace.GotConnInfo) {
			if !connInfo.Reused {
				connDuration = now() - connStart
			}
			reqStart = now()
		},
		WroteRequest: func(w httptrace.WroteRequestInfo) {
			reqDuration = now() - reqStart
			delayStart = now()
		},
		GotFirstResponseByte: func() {
			delayDuration = now() - delayStart
			resStart = now()
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))

	// random part
	if b.RandMark {
		req.URL.Host = strings.Replace(req.URL.Host, "HEY", strconv.Itoa(gort)+"-"+strconv.Itoa(n), -1)
		req.URL.Path = strings.Replace(req.URL.Path, "HEY", strconv.Itoa(gort)+"-"+strconv.Itoa(n), -1)

		for k, v := range req.Header {
			tempv := []string{}
			for _, vv := range v {
				tempv = append(tempv, strings.Replace(vv, "HEY", strconv.Itoa(gort)+"-"+strconv.Itoa(n), -1))
			}
			req.Header[k] = tempv
		}

		body := strings.Replace(b.RequestBody, "HEY", strconv.Itoa(gort)+"-"+strconv.Itoa(n), -1)
		req.Body = ioutil.NopCloser(bytes.NewReader([]byte(body)))

		req.ContentLength = int64(len(body))
	}
	//

	resp, err := c.Do(req)
	if err == nil {
		size = resp.ContentLength
		code = resp.StatusCode
		// bodybyte, _ = ioutil.ReadAll(resp.Body)
		// fmt.Println(string(bodybyte), "=====3")
		io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}

	t := now()
	resDuration = t - resStart
	finish := t - s
	b.results <- &result{
		offset:        s,
		statusCode:    code,
		duration:      finish,
		err:           err,
		contentLength: size,
		connDuration:  connDuration,
		dnsDuration:   dnsDuration,
		reqDuration:   reqDuration,
		resDuration:   resDuration,
		delayDuration: delayDuration,
	}
}

func (b *Work) runWorker(client *http.Client, gort, n int) {
	var throttle <-chan time.Time
	if b.QPS > 0 {
		throttle = time.Tick(time.Duration(1e6/(b.QPS)) * time.Microsecond) // 1e6/(b.QPS) 100w毫秒即1秒 / 1秒运行多少次= 一次运行的时间 即每次需要间隔多久才能达到这个qps
	}

	if b.DisableRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	for i := 0; i < n; i++ {
		// Check if application is stopped. Do not send into a closed channel.
		select {
		case <-b.stopCh:
			return
		default:
			if b.QPS > 0 {
				<-throttle //外层有N个runWorker的并发数，此函数是一个worker要访问多少次，如果没有sleep就一股脑发过去了
				//如果通过sleep变相控制了每秒访问的数量因此-n 1000 -c 100 -q 2 则是一秒访问100*2次 且 c * q < n ，否则n太小的话不到1s没意义，qps也不宜过大，超过本身性能极限，具体真实值查看  Requests/sec
			}
			b.makeRequest(gort, i, client)
		}
	}
}

func (b *Work) runWorkers() {
	tr := http.Transport{}
	certs := tls.Certificate{}
	if b.Certfile != "" && b.Keyfile != "" {
		certstmp, err := tls.LoadX509KeyPair(b.Certfile, b.Keyfile)
		if err != nil {
			fmt.Println(err)
		} else {
			certs = certstmp
		}
		ca, err := x509.ParseCertificate(certs.Certificate[0])
		if err != nil {
			fmt.Println(err)
		}
		pool := x509.NewCertPool()
		pool.AddCert(ca)

		tr = http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      pool,
				Certificates: []tls.Certificate{certs},

				InsecureSkipVerify: true,
				ServerName:         b.Request.Host,
			},
			MaxIdleConnsPerHost: min(b.C, maxIdleConn),
			DisableCompression:  b.DisableCompression,
			DisableKeepAlives:   b.DisableKeepAlives,
			Proxy:               http.ProxyURL(b.ProxyAddr),
		}
	} else {
		tr = http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				ServerName:         b.Request.Host,
			},
			MaxIdleConnsPerHost: min(b.C, maxIdleConn),
			DisableCompression:  b.DisableCompression,
			DisableKeepAlives:   b.DisableKeepAlives,
			Proxy:               http.ProxyURL(b.ProxyAddr),
		}
	}

	if b.H2 {
		http2.ConfigureTransport(&tr)
	} else {
		tr.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	}
	client := &http.Client{Transport: &tr, Timeout: time.Duration(b.Timeout) * time.Second}

	// Ignore the case where b.N % b.C != 0.
	var wg sync.WaitGroup
	wg.Add(b.C)
	for gort := 0; gort < b.C; gort++ {
		go func(gr int) {
			b.runWorker(client, gr, b.N/b.C) //注意此处去余了，也就是Ignore the case where b.N % b.C != 0
			wg.Done()
		}(gort)
	}
	wg.Wait()
}

// cloneRequest returns a clone of the provided *http.Request.
// The clone is a shallow copy of the struct and its Header map.
func cloneRequest(r *http.Request, body string) *http.Request {
	// shallow copy of the struct
	r2 := new(http.Request)
	*r2 = *r
	// deep copy of the Header
	r2.Header = make(http.Header, len(r.Header))
	for k, s := range r.Header {
		r2.Header[k] = append([]string(nil), s...)
	}
	if len(body) > 0 {
		r2.Body = ioutil.NopCloser(bytes.NewReader([]byte(body)))
	}

	return r2
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
