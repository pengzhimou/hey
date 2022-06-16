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

// Command hey is an HTTP load generator.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	gourl "net/url"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/pengzhimou/hey/requester"
)

const (
	headerRegexp = `^([\w-]+):\s*(.+)`
	authRegexp   = `^(.+):([^\s].+)`
	heyUA        = "hey/0.0.2"
)

var (
	m = flag.String("m", "GET", "")
	// headers     = flag.String("h", "", "")
	body        = flag.String("d", "", "")
	bodyFile    = flag.String("D", "", "")
	accept      = flag.String("A", "", "")
	contentType = flag.String("T", "text/html", "")
	authHeader  = flag.String("a", "", "")
	hostHeader  = flag.String("host", "", "")
	userAgent   = flag.String("U", "", "")
	output      = flag.String("o", "", "")
	certfile    = flag.String("cert", "", "")
	keyfile     = flag.String("key", "", "")

	c = flag.Int("c", 50, "")
	n = flag.Int("n", 200, "")
	q = flag.Float64("q", 0, "")
	t = flag.Int("t", 20, "")
	z = flag.Duration("z", 0, "")

	h2   = flag.Bool("h2", false, "")
	cpus = flag.Int("cpus", runtime.GOMAXPROCS(-1), "")

	disableCompression = flag.Bool("disable-compression", false, "")
	disableKeepAlives  = flag.Bool("disable-keepalive", false, "")
	disableRedirects   = flag.Bool("disable-redirects", false, "")
	proxyAddr          = flag.String("x", "", "")
	urlFile            = flag.String("urlfile", "", "")
	url                = flag.String("url", "", "")
	round              = flag.Int("r", 1, "")
	roundsleep         = flag.Int("rs", 0, "")
	randmark           = flag.String("randmark", "", "")
)

var usage = `Usage: hey [options...]

Options:
  -n  Number of requests to run. Default is 200.
  -c  Number of workers to run concurrently. Total number of requests cannot
      be smaller than the concurrency level. Default is 50. Will ignore when -q used.
  -q  Rate limit, in queries per second (QPS). Default is no rate limit. Can't use with -c.
  -z  Duration of application to send requests. When duration is reached,
      application stops and exits. If duration is specified, n is ignored.
      Examples: -z 10s -z 3m.
  -o  Output type. If none provided, a summary is printed.
      "csv" is the only supported alternative. Dumps the response
      metrics in comma-separated values format.

  -m  HTTP method, one of GET, POST, PUT, DELETE, HEAD, OPTIONS.
  -H  Custom HTTP header. You can specify as many as needed by repeating the flag.
      For example, -H "Accept: text/html" -H "Content-Type: application/xml" .
  -t  Timeout for each request in seconds. Default is 20, use 0 for infinite.
  -A  HTTP Accept header.
  -d  HTTP request body, better with -randmark.
  -D  HTTP request body from file. better with -randmark.
  -T  Content-type, defaults to "text/html".
  -U  User-Agent, defaults to version "hey/0.0.2".
  -a  Basic authentication, username:password.
  -x  HTTP Proxy address as host:port.
  -h2 Enable HTTP/2.

  -host	HTTP Host header.

  -disable-compression  Disable compression.
  -disable-keepalive    Disable keep-alive, prevents re-use of TCP
                        connections between different HTTP requests.
  -disable-redirects    Disable following of HTTP redirects
  -cpus                 Number of used cpu cores.
                        (default for current machine is %d cores)

  -cert certfile location
  -key keyfile location
  -urlfile urlfile location
  -url url link
  -r rounds, should with method GET only
  -rs each round skip time, should with method GET only
  -randmark replace HEY mark from url, header, payload with goroutine number
  -respcheck check response body, like -respcheck "\"code\":201" -respcheck "\"msg\":\"good\""
`

func main() {
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, fmt.Sprintf(usage, runtime.NumCPU()))
	}

	// var hs headerSlice
	hs := make(headerSlice, 0)
	flag.Var(&hs, "H", "")

	rc := make(respCheck, 0)
	flag.Var(&rc, "respcheck", "")

	flag.Parse()

	// 没有 <url> 的，现已经切换为-url 不需要此处逻辑
	// if flag.NArg() < 1 {
	// 	usageAndExit("")
	// }

	if flag.NFlag() < 1 {
		usageAndExit("")
	}

	runtime.GOMAXPROCS(*cpus)
	num := *n
	conc := *c
	q := *q
	dur := *z

	if dur > 0 { //当有 -z的时候，-n失效，会默认给一个极大值2147483647
		num = math.MaxInt32
		if conc <= 0 {
			usageAndExit("-c cannot be smaller than 1.")
		}
	} else {
		if num <= 0 || conc <= 0 {
			usageAndExit("-n and -c cannot be smaller than 1.")
		}

		if num < conc {
			usageAndExit("-n cannot be less than -c.")
		}
	}

	// url := flag.Args()[0]
	method := strings.ToUpper(*m)

	// set content-type
	header := make(http.Header)
	header.Set("Content-Type", *contentType)
	// set any other additional headers
	// if *headers != "" {
	// 	usageAndExit("Flag '-h' is deprecated, please use '-H' instead.")
	// }
	// set any other additional repeatable headers
	for _, h := range hs {
		match, err := parseInputWithRegexp(h, headerRegexp)
		if err != nil {
			usageAndExit(err.Error())
		}
		header.Set(match[1], match[2])
	}

	if *accept != "" {
		header.Set("Accept", *accept)
	}

	// set basic auth if set
	var username, password string
	if *authHeader != "" {
		match, err := parseInputWithRegexp(*authHeader, authRegexp)
		if err != nil {
			usageAndExit(err.Error())
		}
		username, password = match[1], match[2]
	}

	var bodyAll string
	if *body != "" {
		bodyAll = *body
	}
	if *bodyFile != "" {
		slurp, err := ioutil.ReadFile(*bodyFile)
		if err != nil {
			errAndExit(err.Error())
		}
		bodyAll = string(slurp)
	}

	var proxyURL *gourl.URL
	if *proxyAddr != "" {
		var err error
		proxyURL, err = gourl.Parse(*proxyAddr)
		if err != nil {
			usageAndExit(err.Error())
		}
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	brk := false
	for r := 0; r <= *round; r++ {

		if r != *round {
			select {
			case <-c:
				brk = true
				break
			default:
				jobFunc(method, *url, bodyAll, header, username, password, num, conc, q, proxyURL, dur, &rc)
				if *round > 1 {
					fmt.Printf("Finished Round: %v, start to sleep:%v second\n", r+1, *roundsleep)
					fmt.Println("---------------------------------")
				}
			}
			if brk {
				break
			}
			time.Sleep(time.Duration(*roundsleep) * time.Second)
		}
	}
}

func jobFunc(method string, url string, bodyAll string, header http.Header, username, password string, num, conc int, q float64, proxyURL *gourl.URL, dur time.Duration, rc *respCheck) {
	wg := sync.WaitGroup{}
	if *urlFile == "" {
		wg.Add(1)
		go requestFunc(method, url, bodyAll, header, username, password, num, conc, q, proxyURL, dur, &wg, rc)
		wg.Wait()
	} else {
		data, err := ioutil.ReadFile(*urlFile)
		if err != nil {
			errAndExit(fmt.Sprintf("---read fail: %s", err.Error()))
		}
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "http") { //处理空行和换行符
				continue
			}
			wg.Add(1)
			go requestFunc(method, line, bodyAll, header, username, password, num, conc, q, proxyURL, dur, &wg, rc)
		}
		wg.Wait()
	}
}

func requestFunc(method string, url string, bodyAll string, header http.Header, username, password string, num, conc int, q float64, proxyURL *gourl.URL, dur time.Duration, waitg *sync.WaitGroup, rc *respCheck) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		usageAndExit(err.Error())
	}
	req.ContentLength = int64(len(bodyAll))
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}

	// set host header if set
	if *hostHeader != "" {
		req.Host = *hostHeader
	}

	ua := header.Get("User-Agent")
	if ua == "" {
		ua = heyUA
	} else {
		ua += " " + heyUA
	}
	header.Set("User-Agent", ua)

	// set userAgent header if set
	if *userAgent != "" {
		ua = *userAgent + " " + heyUA
		header.Set("User-Agent", ua)
	}

	req.Header = header

	w := &requester.Work{
		Request:            req,
		RequestBody:        bodyAll,
		N:                  num,
		C:                  conc,
		QPS:                q,
		Timeout:            *t,
		DisableCompression: *disableCompression,
		DisableKeepAlives:  *disableKeepAlives,
		DisableRedirects:   *disableRedirects,
		H2:                 *h2,
		ProxyAddr:          proxyURL,
		Output:             *output,
		Certfile:           *certfile,
		Keyfile:            *keyfile,
		RandMark:           *randmark,
		RespCheck:          *rc,
	}
	// 初始化results 和stopCh
	w.Init()

	// 处理用户终止ctrl-c，调用stop
	userKill(w)

	// 与-n 次数互斥，为运行的时间到了之后的handle
	if dur > 0 {
		go func() {
			time.Sleep(dur)
			w.Stop()
		}()
	}

	w.Run()

	defer waitg.Done()
}

func userKill(w *requester.Work) {
	// 处理用户终止ctrl-c，调用stop
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		w.Stop()
	}()
}

func errAndExit(msg string) {
	fmt.Fprintf(os.Stderr, msg)
	fmt.Fprintf(os.Stderr, "\n")
	os.Exit(1)
}

func usageAndExit(msg string) {
	if msg != "" {
		fmt.Fprintf(os.Stderr, msg)
		fmt.Fprintf(os.Stderr, "\n\n")
	}
	flag.Usage()
	fmt.Fprintf(os.Stderr, "\n")
	os.Exit(1)
}

func parseInputWithRegexp(input, regx string) ([]string, error) {
	re := regexp.MustCompile(regx)
	matches := re.FindStringSubmatch(input)
	if len(matches) < 1 {
		return nil, fmt.Errorf("could not parse the provided input; input = %v", input)
	}
	return matches, nil
}

type headerSlice []string

func (h *headerSlice) String() string {
	return fmt.Sprintf("%s", *h)
}

func (h *headerSlice) Set(value string) error {
	*h = append(*h, value)
	return nil
}

type respCheck []string

func (h *respCheck) String() string {
	return fmt.Sprintf("%s", *h)
}

func (h *respCheck) Set(value string) error {
	if value != "" {
		*h = append(*h, value)
		// vslc := strings.Split(value, ":")
		// (*h)[strip(vslc[0], " ")] = strip(vslc[1], " ")
	}
	return nil
}

func strip(s_ string, chars_ string) string {
	s, chars := []rune(s_), []rune(chars_)
	length := len(s)
	max := len(s) - 1
	l, r := true, true //标记当左端或者右端找到正常字符后就停止继续寻找
	start, end := 0, max
	tmpEnd := 0
	charset := make(map[rune]bool) //创建字符集，也就是唯一的字符，方便后面判断是否存在
	for i := 0; i < len(chars); i++ {
		charset[chars[i]] = true
	}
	for i := 0; i < length; i++ {
		if _, exist := charset[s[i]]; l && !exist {
			start = i
			l = false
		}
		tmpEnd = max - i
		if _, exist := charset[s[tmpEnd]]; r && !exist {
			end = tmpEnd
			r = false
		}
		if !l && !r {
			break
		}
	}
	if l && r { // 如果左端和右端都没找到正常字符，那么表示该字符串没有正常字符
		return ""
	}
	return string(s[start : end+1])
}
