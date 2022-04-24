package requester

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v2"
)

type JobsFull struct {
	UID    string `yaml:"-"`
	ConcID string `yaml:"-"`
	Jobs   []Job  `yaml:"jobs"`
}

type Job struct {
	JobName string `yaml:"name"`

	BaseURL     string      `yaml:"baseurl"`
	BaseHeader  http.Header `yaml:"baseheader"`
	BasePayload string      `yaml:"basepayload"`

	URLPH     map[string]string `yaml:"urlph"`
	HeaderPH  map[string]string `yaml:"headerph"`
	PayloadPH map[string]string `yaml:"payloadph"`

	URL     string      `yaml:"-"`
	Header  http.Header `yaml:"-"`
	Payload string      `yaml:"-"`
	Methord string      `yaml:"methord"`

	PostSleep int `yaml:"postsleep"`
}

func (jf *JobsFull) Init() {
	jf.UID = uuidShort()
	for _, j := range jf.Jobs {
		// header part
		for ph, _ := range j.HeaderPH {
			for k, v := range j.BaseHeader {
				newK := strings.Replace(k, ph+"-PHYYY", jf.UID, -1) // 如没有匹配成功则不会修改，返回原值
				// newV := strings.Replace(v, ph, jf.UID, -1)
				newV := []string{}
				for _, vv := range v {
					newVV := strings.Replace(vv, ph+"-PHYYY", jf.UID, -1)
					newV = append(newV, newVV)
				}
				j.Header[newK] = newV
			}
		}
		// url part
		if len(j.URLPH) > 0 {
			for ph, _ := range j.URLPH {
				j.URL = strings.Replace(j.BaseURL, ph+"-PHYYY", jf.UID, -1)
			}
		}
		// payload part
		if len(j.PayloadPH) > 0 {
			for ph, _ := range j.PayloadPH {
				j.Payload = strings.Replace(j.BasePayload, ph+"-PHYYY", jf.UID, -1)
			}
		}

		xx, _ := json.Marshal(jf)
		fmt.Println(string(xx))
	}
}

func (jf *JobsFull) ConcInit() {
	jf.ConcID = uuidShort()
	for _, j := range jf.Jobs {
		// header part
		for k, v := range j.Header {
			newK := strings.Replace(k, "-PHYYY", jf.ConcID, -1)
			// newV := strings.Replace(v, ph, jf.UID, -1)
			newV := []string{}
			for _, vv := range v {
				newVV := strings.Replace(vv, "-PHYYY", jf.ConcID, -1)
				newV = append(newV, newVV)
			}
			delete(j.Header, k)
			j.Header[newK] = newV
		}
		// url part
		if len(j.URLPH) > 0 {
			j.URL = strings.Replace(j.URL, "-PHYYY", jf.ConcID, -1)
		}
		// payload part
		if len(j.PayloadPH) > 0 {
			j.Payload = strings.Replace(j.BasePayload, "-PHYYY", jf.ConcID, -1)
		}
	}

	xx, _ := json.Marshal(jf)
	fmt.Println(string(xx))
}

func uuidShort() string {
	uuid := uuid.New().String()
	return strings.Split(uuid, "-")[0]
}

func ParseYamlJobs(path string) (*JobsFull, error) {
	jobsall := &JobsFull{}
	if f, err := os.Open(path); err != nil {
		return nil, err
	} else {
		yaml.NewDecoder(f).Decode(jobsall)
	}
	return jobsall, nil
}

// func streamRequestFunc(username, password string, num, conc int, q float64, proxyURL *gourl.URL, dur time.Duration) {
// 	jobsfull, err := requester.ParseYamlJobs(*streamfile)
// 	if err != nil {
// 		panic("can't parse yaml jobs")
// 	}
// 	jobsfull.Init()

// 	for _, job := range jobsfull.Jobs {
// 		requestFunc(job.Methord, job.URL, []byte(job.Payload), job.Header, username, password, num, conc, q, proxyURL, dur, nil)
// 		time.Sleep(time.Second * time.Duration(job.PostSleep))
// 	}
// }
