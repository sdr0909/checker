package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	MaxThreads    = 500
	AliveFileName = "alive.txt"
)

type Proxy struct {
	Addr       string
	AddrParsed *url.URL
}

func getProxy(addr string) (Proxy, error) {
	u, err := url.Parse(addr)
	if err != nil {
		return Proxy{}, fmt.Errorf("cannot parse proxy URL: %w", err)
	}
	return Proxy{
		Addr:       addr,
		AddrParsed: u,
	}, nil
}

func CheckProxies(ctx context.Context, proxies []Proxy, maxConcurrent int) []Proxy {
	alive := make(chan Proxy, len(proxies))
	sem := make(chan struct{}, maxConcurrent)

	var wg sync.WaitGroup
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			MaxVersion:         tls.VersionTLS13,
			InsecureSkipVerify: true,
		},
		DisableKeepAlives: true,
	}}

	for _, proxy := range proxies {
		wg.Add(1)
		go func(p Proxy) {
			defer wg.Done()
			sem <- struct{}{}
			CheckAlive(ctx, client, p, alive)
			<-sem
		}(proxy)
	}
	wg.Wait()
	close(alive)

	aliveProxies := make([]Proxy, 0)
	for proxy := range alive {
		aliveProxies = append(aliveProxies, proxy)
	}

	return aliveProxies
}

func GetProxies(dir string) ([]Proxy, error) {
	result := make([]Proxy, 0)
	content, err := ioutil.ReadFile(dir)
	if err != nil {
		return result, fmt.Errorf("unable to read file with proxies: err = %v", err)
	}
	proxies := strings.Split(string(content), "\n")

	for _, addr := range proxies {
		proxy, err := getProxy(addr)
		if err != nil {
			continue
		}
		result = append(result, proxy)
	}
	if len(result) == 0 {
		return result, fmt.Errorf("unable to find any valid proxy")
	}
	return result, nil
}

func CheckAlive(ctx context.Context, client *http.Client, p Proxy, alive chan<- Proxy) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.ipify.org?format=json", nil)
	if err != nil {
		log.Printf("[%s] => error, internal error: %v", p.Addr, err)
		return
	}

	// if u := p.AddrParsed.User; u != nil {
	// 	password, hasPassword := u.Password()
	// 	if hasPassword {
	// 		creds := u.Username() + ":" + password
	// 		req.Header.Add("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(creds)))
	// 	}
	// }

	client.Transport.(*http.Transport).Proxy = http.ProxyURL(p.AddrParsed)

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[%s] => error: %v", p.Addr, err)
		return
	}
	defer resp.Body.Close()

	log.Printf("[%s] => ok", p.Addr)
	alive <- p
}

func main() {
	proxies, err := GetProxies("ruholysorted.txt")
	if err != nil {
		log.Printf("unable to get proxies: %v", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*2)
	defer cancel()

	alive := CheckProxies(ctx, proxies, MaxThreads)
	if len(alive) == 0 {
		log.Println("no proxies alive")
		return
	}

	d := make([]string, 0, len(alive))
	for _, p := range alive {
		d = append(d, p.Addr)
	}

	err = ioutil.WriteFile(AliveFileName, []byte(strings.Join(d, "\n")), 0660)
	if err != nil {
		log.Printf("unable to write file: %v", err)
		os.Exit(1)
	}
}
