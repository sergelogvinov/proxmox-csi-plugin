/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cluster

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	pxpool "github.com/sergelogvinov/go-proxmox-pool"
	proxmoxrest "github.com/sergelogvinov/go-proxmox-rest"
	"github.com/sergelogvinov/go-proxmox-rest/fakeapi"
)

var guestPath = regexp.MustCompile(`^/nodes/[^/]+/qemu/(\d+)/(config|unlink)$`)

// PendingConfig fronts a fake Proxmox cluster with the pending-config
// semantics fakeapi does not model. The fake's own guest config is the
// current config (GET .../config?current=1). The default view
// (GET .../config) applies the pending changes recorded here on top of it,
// the way Proxmox does: pending values replace or add keys, pending
// deletions hide them.
//
// It also serves PUT .../unlink, which fakeapi lacks, as a config deletion.
type PendingConfig struct {
	mu  sync.Mutex
	vms map[int]*pendingVM

	proxy  *httputil.ReverseProxy
	client *proxmoxrest.Client
}

type pendingVM struct {
	// values holds pending values by config key, e.g. a disk pending addition.
	values map[string]string
	// deletions holds the config keys pending deletion.
	deletions  map[string]bool
	hotplugErr bool
	hotplugs   int
}

// setPending records a pending value for key, replacing a pending deletion.
func (vm *pendingVM) setPending(key, value string) {
	vm.values[key] = value
	delete(vm.deletions, key)
}

// deletePending marks key as pending deletion, replacing a pending value.
func (vm *pendingVM) deletePending(key string) {
	vm.deletions[key] = true
	delete(vm.values, key)
}

// revert discards any pending change of key.
func (vm *pendingVM) revert(key string) {
	delete(vm.values, key)
	delete(vm.deletions, key)
}

// SetupPendingConfig starts a proxy in front of cl and points region's REST
// client in pool at it.
func SetupPendingConfig(t *testing.T, cl *fakeapi.Cluster, pool *pxpool.ProxmoxPool, region string) *PendingConfig {
	t.Helper()

	p := NewPendingConfig(t, cl)
	pool.Set(region, p.Client())

	return p
}

// NewPendingConfig starts a proxy in front of cl.
func NewPendingConfig(t *testing.T, cl *fakeapi.Cluster) *PendingConfig {
	t.Helper()

	cfg := cl.Client(t).ToRESTConfig()

	upstream, err := url.Parse(cfg.BaseURL)
	if err != nil {
		t.Fatalf("pending: parsing fake cluster url: %v", err)
	}

	p := &PendingConfig{
		vms:   map[int]*pendingVM{},
		proxy: httputil.NewSingleHostReverseProxy(upstream),
	}

	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)

	cfg.BaseURL = srv.URL

	p.client, err = proxmoxrest.New(cfg)
	if err != nil {
		t.Fatalf("pending: building client: %v", err)
	}

	return p
}

// Client returns a REST client talking to the proxy.
func (p *PendingConfig) Client() *proxmoxrest.Client {
	return p.client
}

// SetPending records a pending value for key (e.g. "scsi2") of guest vmid,
// as left behind by a hotplug that failed.
func (p *PendingConfig) SetPending(vmid int, key, value string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.vm(vmid).setPending(key, value)
}

// DeletePending marks key of guest vmid as pending deletion, as left behind
// by a hot-unplug that failed.
func (p *PendingConfig) DeletePending(vmid int, key string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.vm(vmid).deletePending(key)
}

// FailHotplug makes every disk hotplug (POST .../config) and hot-unplug
// (PUT .../unlink) on guest vmid fail the way Proxmox does: the change is
// recorded as pending and the request is rejected with "hotplug problem".
func (p *PendingConfig) FailHotplug(vmid int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.vm(vmid).hotplugErr = true
}

// Hotplugs returns how many disk hotplugs (POST .../config with a disk key)
// guest vmid has received, failed or not.
func (p *PendingConfig) Hotplugs(vmid int) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.vm(vmid).hotplugs
}

// ServeHTTP implements http.Handler.
func (p *PendingConfig) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	match := guestPath.FindStringSubmatch(r.URL.Path)
	if match == nil {
		p.proxy.ServeHTTP(w, r)

		return
	}

	vmid, _ := strconv.Atoi(match[1]) //nolint:errcheck // the pattern only matches digits
	endpoint := match[2]

	switch {
	case endpoint == "unlink" && r.Method == http.MethodPut:
		p.unlink(w, r, vmid)
	case endpoint == "config" && r.Method == http.MethodPut:
		p.revert(r, vmid)
		p.proxy.ServeHTTP(w, r)
	case endpoint == "config" && r.Method == http.MethodPost:
		p.revert(r, vmid)
		p.hotplug(w, r, vmid)
	case endpoint == "config" && r.Method == http.MethodGet && r.URL.Query().Get("current") != "1":
		p.pendingView(w, r, vmid)
	default:
		p.proxy.ServeHTTP(w, r)
	}
}

func (p *PendingConfig) vm(vmid int) *pendingVM {
	vm, ok := p.vms[vmid]
	if !ok {
		vm = &pendingVM{values: map[string]string{}, deletions: map[string]bool{}}
		p.vms[vmid] = vm
	}

	return vm
}

// revert discards the pending changes of the keys listed in the request's
// revert parameter, as Proxmox does.
func (p *PendingConfig) revert(r *http.Request, vmid int) {
	keys := r.URL.Query().Get("revert")
	if keys == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if vm, ok := p.vms[vmid]; ok {
		for key := range strings.SplitSeq(keys, ",") {
			vm.revert(strings.TrimSpace(key))
		}
	}
}

// rejectFailingHotplug rejects a disk hotplug or hot-unplug on guest vmid
// with "hotplug problem" if FailHotplug was called for it, after letting
// recordPending record the change as pending. It reports whether it rejected
// the request.
func (p *PendingConfig) rejectFailingHotplug(w http.ResponseWriter, vmid int, recordPending func(vm *pendingVM)) bool {
	p.mu.Lock()
	vm := p.vm(vmid)
	failing := vm.hotplugErr

	if failing {
		recordPending(vm)
	}
	p.mu.Unlock()

	if failing {
		writeError(w, "hotplug problem")
	}

	return failing
}

func (p *PendingConfig) hotplug(w http.ResponseWriter, r *http.Request, vmid int) {
	query := r.URL.Query()

	p.mu.Lock()
	for key := range query {
		if isDiskKey(key) {
			p.vm(vmid).hotplugs++
		}
	}
	p.mu.Unlock()

	rejected := p.rejectFailingHotplug(w, vmid, func(vm *pendingVM) {
		for key, values := range query {
			if isDiskKey(key) && len(values) > 0 {
				vm.setPending(key, values[0])
			}
		}
	})
	if rejected {
		return
	}

	p.proxy.ServeHTTP(w, r)
}

func (p *PendingConfig) unlink(w http.ResponseWriter, r *http.Request, vmid int) {
	ids := strings.Split(r.URL.Query().Get("idlist"), ",")

	rejected := p.rejectFailingHotplug(w, vmid, func(vm *pendingVM) {
		for _, id := range ids {
			vm.deletePending(strings.TrimSpace(id))
		}
	})
	if rejected {
		return
	}

	query := url.Values{}
	query.Set("delete", strings.Join(ids, ","))

	configReq := r.Clone(r.Context())
	configReq.URL.Path = strings.TrimSuffix(r.URL.Path, "/unlink") + "/config"
	configReq.URL.RawQuery = query.Encode()

	p.proxy.ServeHTTP(w, configReq)
}

func (p *PendingConfig) pendingView(w http.ResponseWriter, r *http.Request, vmid int) {
	rec := httptest.NewRecorder()
	p.proxy.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		copyResponse(w, rec)

		return
	}

	var response struct {
		Data map[string]any `json:"data"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.Data == nil {
		copyResponse(w, rec)

		return
	}

	p.mu.Lock()
	if vm, ok := p.vms[vmid]; ok {
		for key, value := range vm.values {
			response.Data[key] = value
		}

		for key := range vm.deletions {
			delete(response.Data, key)
		}
	}
	p.mu.Unlock()

	writeJSON(w, http.StatusOK, response)
}

func isDiskKey(key string) bool {
	for _, prefix := range []string{"scsi", "virtio", "sata", "ide"} {
		if index, ok := strings.CutPrefix(key, prefix); ok {
			if _, err := strconv.Atoi(index); err == nil {
				return true
			}
		}
	}

	return false
}

func copyResponse(w http.ResponseWriter, rec *httptest.ResponseRecorder) {
	maps.Copy(w.Header(), rec.Header())

	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes()) //nolint:errcheck // test proxy
}

func writeError(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"data": nil, "message": message})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body) //nolint:errcheck // test proxy
}
