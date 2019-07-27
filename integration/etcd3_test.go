package integration

import (
	"crypto/tls"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/abronan/valkeyrie"
	"github.com/abronan/valkeyrie/store"
	etcdv3 "github.com/abronan/valkeyrie/store/etcd/v3"
	"github.com/containous/traefik/integration/try"
	"github.com/go-check/check"

	checker "github.com/vdemeester/shakers"
)

const (
	// Services IP addresses fixed in the configuration
	ipEtcd     = "172.18.0.2"
	ipWhoami01 = "172.18.0.3"
	ipWhoami02 = "172.18.0.4"
	ipWhoami03 = "172.18.0.5"
	ipWhoami04 = "172.18.0.6"

	traefikEtcdURL    = "http://127.0.0.1:8000/"
	traefikWebEtcdURL = "http://127.0.0.1:8081/"
)

// Etcd test suites (using libcompose)
type Etcd3Suite struct {
	BaseSuite
	kv store.Store
}

func (s *Etcd3Suite) SetUpTest(c *check.C) {
	s.createComposeProject(c, "etcd3")
	s.composeProject.Start(c)

	etcdv3.Register()
	url := ipEtcd + ":2379"
	kv, err := valkeyrie.NewStore(
		store.ETCDV3,
		[]string{url},
		&store.Config{
			ConnectionTimeout: 30 * time.Second,
		},
	)
	if err != nil {
		c.Fatal("Cannot create store etcd")
	}
	s.kv = kv

	// wait for etcd
	err = try.Do(60*time.Second, func() error {
		_, err := kv.Exists("test", nil)
		return err
	})
	c.Assert(err, checker.IsNil)
}

func (s *Etcd3Suite) TearDownTest(c *check.C) {
	// shutdown and delete compose project
	if s.composeProject != nil {
		s.composeProject.Stop(c)
	}
}

func (s *Etcd3Suite) TearDownSuite(c *check.C) {}

func (s *Etcd3Suite) TestSimpleConfiguration(c *check.C) {
	file := s.adaptFile(c, "fixtures/etcd/simple.toml", struct {
		EtcdHost string
		UseAPIV3 bool
	}{
		ipEtcd,
		true,
	})
	defer os.Remove(file)

	cmd, display := s.traefikCmd(withConfigFile(file))
	defer display(c)
	err := cmd.Start()
	c.Assert(err, checker.IsNil)
	defer cmd.Process.Kill()

	// TODO validate : run on 80
	// Expected a 404 as we did not configure anything
	err = try.GetRequest(traefikEtcdURL, 1*time.Second, try.StatusCodeIs(http.StatusNotFound))
	c.Assert(err, checker.IsNil)
}

func (s *Etcd3Suite) TestNominalConfiguration(c *check.C) {
	file := s.adaptFile(c, "fixtures/etcd/simple.toml", struct {
		EtcdHost string
		UseAPIV3 bool
	}{
		ipEtcd,
		true,
	})
	defer os.Remove(file)

	cmd, display := s.traefikCmd(withConfigFile(file))
	defer display(c)
	err := cmd.Start()
	c.Assert(err, checker.IsNil)
	defer cmd.Process.Kill()

	backend1 := map[string]string{
		"/traefik/backends/backend1/circuitbreaker/expression": "NetworkErrorRatio() > 0.5",
		"/traefik/backends/backend1/servers/server1/url":       "http://" + ipWhoami01 + ":80",
		"/traefik/backends/backend1/servers/server1/weight":    "10",
		"/traefik/backends/backend1/servers/server2/url":       "http://" + ipWhoami02 + ":80",
		"/traefik/backends/backend1/servers/server2/weight":    "1",
	}
	backend2 := map[string]string{
		"/traefik/backends/backend2/loadbalancer/method":    "drr",
		"/traefik/backends/backend2/servers/server1/url":    "http://" + ipWhoami03 + ":80",
		"/traefik/backends/backend2/servers/server1/weight": "1",
		"/traefik/backends/backend2/servers/server2/url":    "http://" + ipWhoami04 + ":80",
		"/traefik/backends/backend2/servers/server2/weight": "2",
	}
	frontend1 := map[string]string{
		"/traefik/frontends/frontend1/backend":            "backend2",
		"/traefik/frontends/frontend1/entrypoints":        "http",
		"/traefik/frontends/frontend1/priority":           "1",
		"/traefik/frontends/frontend1/routes/test_1/rule": "Host:test.localhost",
	}
	frontend2 := map[string]string{
		"/traefik/frontends/frontend2/backend":            "backend1",
		"/traefik/frontends/frontend2/entrypoints":        "http",
		"/traefik/frontends/frontend2/priority":           "10",
		"/traefik/frontends/frontend2/routes/test_2/rule": "Path:/test",
	}
	for key, value := range backend1 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range backend2 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range frontend1 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range frontend2 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}

	// wait for etcd
	err = try.Do(60*time.Second, func() error {
		_, err := s.kv.Exists("/traefik/frontends/frontend2/routes/test_2/rule", nil)
		return err
	})
	c.Assert(err, checker.IsNil)

	// wait for traefik
	err = try.GetRequest(traefikWebEtcdURL+"api/providers", 60*time.Second, try.BodyContains("Path:/test"))
	c.Assert(err, checker.IsNil)

	client := &http.Client{}
	req, err := http.NewRequest(http.MethodGet, traefikEtcdURL, nil)
	c.Assert(err, checker.IsNil)
	req.Host = "test.localhost"
	response, err := client.Do(req)

	c.Assert(err, checker.IsNil)
	c.Assert(response.StatusCode, checker.Equals, http.StatusOK)

	body, err := ioutil.ReadAll(response.Body)
	c.Assert(err, checker.IsNil)
	if !strings.Contains(string(body), ipWhoami03) &&
		!strings.Contains(string(body), ipWhoami04) {
		c.Fail()
	}

	req, err = http.NewRequest(http.MethodGet, traefikEtcdURL+"test", nil)
	c.Assert(err, checker.IsNil)
	response, err = client.Do(req)

	c.Assert(err, checker.IsNil)
	c.Assert(response.StatusCode, checker.Equals, http.StatusOK)

	body, err = ioutil.ReadAll(response.Body)
	c.Assert(err, checker.IsNil)
	if !strings.Contains(string(body), ipWhoami01) &&
		!strings.Contains(string(body), ipWhoami02) {
		c.Fail()
	}

	req, err = http.NewRequest(http.MethodGet, traefikEtcdURL+"test2", nil)
	c.Assert(err, checker.IsNil)
	req.Host = "test2.localhost"
	resp, err := client.Do(req)
	c.Assert(err, checker.IsNil)
	c.Assert(resp.StatusCode, checker.Equals, http.StatusNotFound)

	resp, err = http.Get(traefikEtcdURL)
	c.Assert(err, checker.IsNil)
	c.Assert(resp.StatusCode, checker.Equals, http.StatusNotFound)
}

func (s *Etcd3Suite) TestGlobalConfiguration(c *check.C) {
	err := s.kv.Put("/traefik/entrypoints/http/address", []byte(":8001"), nil)
	c.Assert(err, checker.IsNil)

	// wait for etcd
	err = try.Do(60*time.Second, func() error {
		_, err := s.kv.Exists("/traefik/entrypoints/http/address", nil)
		return err
	})
	c.Assert(err, checker.IsNil)

	// start traefik
	cmd, display := s.traefikCmd(
		withConfigFile("fixtures/simple_web.toml"),
		"--etcd",
		"--etcd.endpoint="+ipEtcd+":4001",
		"--etcd.useAPIV3=true")
	defer display(c)
	err = cmd.Start()
	c.Assert(err, checker.IsNil)
	defer cmd.Process.Kill()

	backend1 := map[string]string{
		"/traefik/backends/backend1/circuitbreaker/expression": "NetworkErrorRatio() > 0.5",
		"/traefik/backends/backend1/servers/server1/url":       "http://" + ipWhoami01 + ":80",
		"/traefik/backends/backend1/servers/server1/weight":    "10",
		"/traefik/backends/backend1/servers/server2/url":       "http://" + ipWhoami02 + ":80",
		"/traefik/backends/backend1/servers/server2/weight":    "1",
	}
	backend2 := map[string]string{
		"/traefik/backends/backend2/loadbalancer/method":    "drr",
		"/traefik/backends/backend2/servers/server1/url":    "http://" + ipWhoami03 + ":80",
		"/traefik/backends/backend2/servers/server1/weight": "1",
		"/traefik/backends/backend2/servers/server2/url":    "http://" + ipWhoami04 + ":80",
		"/traefik/backends/backend2/servers/server2/weight": "2",
	}
	frontend1 := map[string]string{
		"/traefik/frontends/frontend1/backend":            "backend2",
		"/traefik/frontends/frontend1/entrypoints":        "http",
		"/traefik/frontends/frontend1/priority":           "1",
		"/traefik/frontends/frontend1/routes/test_1/rule": "Host:test.localhost",
	}
	frontend2 := map[string]string{
		"/traefik/frontends/frontend2/backend":            "backend1",
		"/traefik/frontends/frontend2/entrypoints":        "http",
		"/traefik/frontends/frontend2/priority":           "10",
		"/traefik/frontends/frontend2/routes/test_2/rule": "Path:/test",
	}
	for key, value := range backend1 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range backend2 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range frontend1 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range frontend2 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}

	// wait for etcd
	err = try.Do(60*time.Second, func() error {
		_, err := s.kv.Exists("/traefik/frontends/frontend2/routes/test_2/rule", nil)
		return err
	})
	c.Assert(err, checker.IsNil)

	// wait for traefik
	err = try.GetRequest("http://127.0.0.1:8080/api/providers", 60*time.Second, try.BodyContains("Path:/test"))
	c.Assert(err, checker.IsNil)

	// check
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8001/", nil)
	c.Assert(err, checker.IsNil)
	req.Host = "test.localhost"

	err = try.Request(req, 500*time.Millisecond, try.StatusCodeIs(http.StatusOK))
	c.Assert(err, checker.IsNil)
}

func (s *Etcd3Suite) TestCertificatesContentWithSNIConfigHandshake(c *check.C) {
	// start traefik
	cmd, display := s.traefikCmd(
		withConfigFile("fixtures/simple_web.toml"),
		"--etcd",
		"--etcd.endpoint="+ipEtcd+":4001",
		"--etcd.useAPIV3=true")
	defer display(c)

	// Copy the contents of the certificate files into ETCD
	snitestComCert, err := ioutil.ReadFile("fixtures/https/snitest.com.cert")
	c.Assert(err, checker.IsNil)
	snitestComKey, err := ioutil.ReadFile("fixtures/https/snitest.com.key")
	c.Assert(err, checker.IsNil)
	snitestOrgCert, err := ioutil.ReadFile("fixtures/https/snitest.org.cert")
	c.Assert(err, checker.IsNil)
	snitestOrgKey, err := ioutil.ReadFile("fixtures/https/snitest.org.key")
	c.Assert(err, checker.IsNil)

	globalConfig := map[string]string{
		"/traefik/entrypoints/https/address":                     ":4443",
		"/traefik/entrypoints/https/tls/certificates/0/certfile": string(snitestComCert),
		"/traefik/entrypoints/https/tls/certificates/0/keyfile":  string(snitestComKey),
		"/traefik/entrypoints/https/tls/certificates/1/certfile": string(snitestOrgCert),
		"/traefik/entrypoints/https/tls/certificates/1/keyfile":  string(snitestOrgKey),
		"/traefik/defaultentrypoints/0":                          "https",
	}

	backend1 := map[string]string{
		"/traefik/backends/backend1/circuitbreaker/expression": "NetworkErrorRatio() > 0.5",
		"/traefik/backends/backend1/servers/server1/url":       "http://" + ipWhoami01 + ":80",
		"/traefik/backends/backend1/servers/server1/weight":    "10",
		"/traefik/backends/backend1/servers/server2/url":       "http://" + ipWhoami02 + ":80",
		"/traefik/backends/backend1/servers/server2/weight":    "1",
	}
	backend2 := map[string]string{
		"/traefik/backends/backend2/loadbalancer/method":    "drr",
		"/traefik/backends/backend2/servers/server1/url":    "http://" + ipWhoami03 + ":80",
		"/traefik/backends/backend2/servers/server1/weight": "1",
		"/traefik/backends/backend2/servers/server2/url":    "http://" + ipWhoami04 + ":80",
		"/traefik/backends/backend2/servers/server2/weight": "2",
	}
	frontend1 := map[string]string{
		"/traefik/frontends/frontend1/backend":            "backend2",
		"/traefik/frontends/frontend1/entrypoints":        "http",
		"/traefik/frontends/frontend1/priority":           "1",
		"/traefik/frontends/frontend1/routes/test_1/rule": "Host:snitest.com",
	}
	frontend2 := map[string]string{
		"/traefik/frontends/frontend2/backend":            "backend1",
		"/traefik/frontends/frontend2/entrypoints":        "http",
		"/traefik/frontends/frontend2/priority":           "10",
		"/traefik/frontends/frontend2/routes/test_2/rule": "Host:snitest.org",
	}
	for key, value := range globalConfig {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range backend1 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range backend2 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range frontend1 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range frontend2 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}

	// wait for etcd
	err = try.Do(60*time.Second, try.KVExists(s.kv, "/traefik/frontends/frontend1/backend"))
	c.Assert(err, checker.IsNil)

	err = cmd.Start()
	c.Assert(err, checker.IsNil)
	defer cmd.Process.Kill()

	// wait for traefik
	err = try.GetRequest("http://127.0.0.1:8080/api/providers", 60*time.Second, try.BodyContains("Host:snitest.org"))
	c.Assert(err, checker.IsNil)

	// check
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "snitest.com",
	}
	conn, err := tls.Dial("tcp", "127.0.0.1:4443", tlsConfig)
	c.Assert(err, checker.IsNil, check.Commentf("failed to connect to server"))

	defer conn.Close()
	err = conn.Handshake()
	c.Assert(err, checker.IsNil, check.Commentf("TLS handshake error"))

	cs := conn.ConnectionState()
	err = cs.PeerCertificates[0].VerifyHostname("snitest.com")
	c.Assert(err, checker.IsNil, check.Commentf("certificate did not match SNI servername"))
}

func (s *Etcd3Suite) TestCommandStoreConfig(c *check.C) {
	cmd, display := s.traefikCmd(
		"storeconfig",
		withConfigFile("fixtures/simple_web.toml"),
		"--etcd.endpoint="+ipEtcd+":4001",
		"--etcd.useAPIV3=true")
	defer display(c)
	err := cmd.Start()
	c.Assert(err, checker.IsNil)

	// wait for traefik finish without error
	cmd.Wait()

	// CHECK
	checkmap := map[string]string{
		"/traefik/loglevel":                 "DEBUG",
		"/traefik/defaultentrypoints/0":     "http",
		"/traefik/entrypoints/http/address": ":8000",
		"/traefik/api/entrypoint":           "traefik",
		"/traefik/etcd/endpoint":            ipEtcd + ":4001",
	}

	for key, value := range checkmap {
		var p *store.KVPair
		err = try.Do(60*time.Second, func() error {
			p, err = s.kv.Get(key, nil)
			return err
		})
		c.Assert(err, checker.IsNil)

		c.Assert(string(p.Value), checker.Equals, value)
	}
}

func (s *Etcd3Suite) TestSNIDynamicTlsConfig(c *check.C) {
	// start Traefik
	cmd, display := s.traefikCmd(
		withConfigFile("fixtures/etcd/simple_https.toml"),
		"--etcd",
		"--etcd.endpoint="+ipEtcd+":4001",
		"--etcd.useAPIV3=true")
	defer display(c)

	snitestComCert, err := ioutil.ReadFile("fixtures/https/snitest.com.cert")
	c.Assert(err, checker.IsNil)
	snitestComKey, err := ioutil.ReadFile("fixtures/https/snitest.com.key")
	c.Assert(err, checker.IsNil)
	snitestOrgCert, err := ioutil.ReadFile("fixtures/https/snitest.org.cert")
	c.Assert(err, checker.IsNil)
	snitestOrgKey, err := ioutil.ReadFile("fixtures/https/snitest.org.key")
	c.Assert(err, checker.IsNil)

	backend1 := map[string]string{
		"/traefik/backends/backend1/circuitbreaker/expression": "NetworkErrorRatio() > 0.5",
		"/traefik/backends/backend1/servers/server1/url":       "http://" + ipWhoami01 + ":80",
		"/traefik/backends/backend1/servers/server1/weight":    "10",
		"/traefik/backends/backend1/servers/server2/url":       "http://" + ipWhoami02 + ":80",
		"/traefik/backends/backend1/servers/server2/weight":    "1",
	}
	backend2 := map[string]string{
		"/traefik/backends/backend2/loadbalancer/method":    "drr",
		"/traefik/backends/backend2/servers/server1/url":    "http://" + ipWhoami03 + ":80",
		"/traefik/backends/backend2/servers/server1/weight": "1",
		"/traefik/backends/backend2/servers/server2/url":    "http://" + ipWhoami04 + ":80",
		"/traefik/backends/backend2/servers/server2/weight": "2",
	}
	frontend1 := map[string]string{
		"/traefik/frontends/frontend1/backend":            "backend2",
		"/traefik/frontends/frontend1/entrypoints":        "https",
		"/traefik/frontends/frontend1/priority":           "1",
		"/traefik/frontends/frontend1/routes/test_1/rule": "Host:snitest.com",
	}

	frontend2 := map[string]string{
		"/traefik/frontends/frontend2/backend":            "backend1",
		"/traefik/frontends/frontend2/entrypoints":        "https",
		"/traefik/frontends/frontend2/priority":           "10",
		"/traefik/frontends/frontend2/routes/test_2/rule": "Host:snitest.org",
	}

	tlsconfigure1 := map[string]string{
		"/traefik/tls/snitestcom/entrypoints":          "https",
		"/traefik/tls/snitestcom/certificate/keyfile":  string(snitestComKey),
		"/traefik/tls/snitestcom/certificate/certfile": string(snitestComCert),
	}

	tlsconfigure2 := map[string]string{
		"/traefik/tls/snitestorg/entrypoints":          "https",
		"/traefik/tls/snitestorg/certificate/keyfile":  string(snitestOrgKey),
		"/traefik/tls/snitestorg/certificate/certfile": string(snitestOrgCert),
	}

	// config backends,frontends and first tls keypair
	for key, value := range backend1 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range backend2 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range frontend1 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range frontend2 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range tlsconfigure1 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}

	tr1 := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "snitest.com",
		},
	}

	tr2 := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "snitest.org",
		},
	}

	// wait for etcd
	err = try.Do(60*time.Second, func() error {
		_, err := s.kv.Get("/traefik/tls/snitestcom/certificate/keyfile", nil)
		return err
	})
	c.Assert(err, checker.IsNil)

	err = cmd.Start()
	c.Assert(err, checker.IsNil)
	defer cmd.Process.Kill()

	req, err := http.NewRequest(http.MethodGet, "https://127.0.0.1:4443/", nil)
	c.Assert(err, checker.IsNil)
	req.Host = tr1.TLSClientConfig.ServerName
	req.Header.Set("Host", tr1.TLSClientConfig.ServerName)
	req.Header.Set("Accept", "*/*")

	err = try.RequestWithTransport(req, 30*time.Second, tr1, try.HasCn(tr1.TLSClientConfig.ServerName))
	c.Assert(err, checker.IsNil)

	// now we configure the second keypair in etcd and the request for host "snitest.org" will use the second keypair

	for key, value := range tlsconfigure2 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}

	// wait for etcd
	err = try.Do(60*time.Second, func() error {
		_, err := s.kv.Get("/traefik/tls/snitestorg/certificate/keyfile", nil)
		return err
	})
	c.Assert(err, checker.IsNil)

	req, err = http.NewRequest(http.MethodGet, "https://127.0.0.1:4443/", nil)
	c.Assert(err, checker.IsNil)
	req.Host = tr2.TLSClientConfig.ServerName
	req.Header.Set("Host", tr2.TLSClientConfig.ServerName)
	req.Header.Set("Accept", "*/*")

	err = try.RequestWithTransport(req, 30*time.Second, tr2, try.HasCn(tr2.TLSClientConfig.ServerName))
	c.Assert(err, checker.IsNil)
}

func (s *Etcd3Suite) TestDeleteSNIDynamicTlsConfig(c *check.C) {
	// start Traefik
	cmd, display := s.traefikCmd(
		withConfigFile("fixtures/etcd/simple_https.toml"),
		"--etcd",
		"--etcd.endpoint="+ipEtcd+":4001",
		"--etcd.useAPIV3=true")
	defer display(c)

	// prepare to config
	snitestComCert, err := ioutil.ReadFile("fixtures/https/snitest.com.cert")
	c.Assert(err, checker.IsNil)
	snitestComKey, err := ioutil.ReadFile("fixtures/https/snitest.com.key")
	c.Assert(err, checker.IsNil)

	backend1 := map[string]string{
		"/traefik/backends/backend1/circuitbreaker/expression": "NetworkErrorRatio() > 0.5",
		"/traefik/backends/backend1/servers/server1/url":       "http://" + ipWhoami01 + ":80",
		"/traefik/backends/backend1/servers/server1/weight":    "1",
		"/traefik/backends/backend1/servers/server2/url":       "http://" + ipWhoami02 + ":80",
		"/traefik/backends/backend1/servers/server2/weight":    "1",
	}

	frontend1 := map[string]string{
		"/traefik/frontends/frontend1/backend":            "backend1",
		"/traefik/frontends/frontend1/entrypoints":        "https",
		"/traefik/frontends/frontend1/priority":           "1",
		"/traefik/frontends/frontend1/routes/test_1/rule": "Host:snitest.com",
	}

	tlsconfigure1 := map[string]string{
		"/traefik/tls/snitestcom/entrypoints":          "https",
		"/traefik/tls/snitestcom/certificate/keyfile":  string(snitestComKey),
		"/traefik/tls/snitestcom/certificate/certfile": string(snitestComCert),
	}

	// config backends,frontends and first tls keypair
	for key, value := range backend1 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range frontend1 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}
	for key, value := range tlsconfigure1 {
		err := s.kv.Put(key, []byte(value), nil)
		c.Assert(err, checker.IsNil)
	}

	tr1 := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "snitest.com",
		},
	}

	// wait for etcd
	err = try.Do(60*time.Second, func() error {
		_, err := s.kv.Get("/traefik/tls/snitestcom/certificate/keyfile", nil)
		return err
	})
	c.Assert(err, checker.IsNil)

	err = cmd.Start()
	c.Assert(err, checker.IsNil)
	defer cmd.Process.Kill()

	req, err := http.NewRequest(http.MethodGet, "https://127.0.0.1:4443/", nil)
	c.Assert(err, checker.IsNil)
	req.Host = tr1.TLSClientConfig.ServerName
	req.Header.Set("Host", tr1.TLSClientConfig.ServerName)
	req.Header.Set("Accept", "*/*")

	err = try.RequestWithTransport(req, 30*time.Second, tr1, try.HasCn(tr1.TLSClientConfig.ServerName))
	c.Assert(err, checker.IsNil)

	// now we delete the tls cert/key pairs,so the endpoint show use default cert/key pair
	for key := range tlsconfigure1 {
		err := s.kv.Delete(key)
		c.Assert(err, checker.IsNil)
	}

	req, err = http.NewRequest(http.MethodGet, "https://127.0.0.1:4443/", nil)
	c.Assert(err, checker.IsNil)
	req.Host = tr1.TLSClientConfig.ServerName
	req.Header.Set("Host", tr1.TLSClientConfig.ServerName)
	req.Header.Set("Accept", "*/*")

	err = try.RequestWithTransport(req, 30*time.Second, tr1, try.HasCn("TRAEFIK DEFAULT CERT"))
	c.Assert(err, checker.IsNil)
}
