/*
Portions Copyright 2017 Istio Authors
Portions Copyright 2017 Aspen Mesh Authors.

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

// Package util provides common constants and helper functions for vetters.
package util

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cnf/structhash"
	"github.com/ghodss/yaml"
	"github.com/golang/glog"
	proxyconfig "istio.io/api/proxy/v1/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	apiv1 "github.com/aspenmesh/istio-vet/api/v1"
)

const (
	IstioNamespace                = "istio-system"
	IstioProxyContainerName       = "istio-proxy"
	IstioMixerDeploymentName      = "istio-mixer"
	IstioMixerContainerName       = "mixer"
	IstioPilotDeploymentName      = "istio-pilot"
	IstioPilotContainerName       = "discovery"
	IstioConfigMap                = "istio"
	IstioConfigMapKey             = "mesh"
	IstioAuthPolicy               = "authPolicy: MUTUAL_TLS"
	IstioInitializerPodAnnotation = "sidecar.istio.io/status"
	IstioInitializerConfigMap     = "istio-inject"
	IstioInitializerConfigMapKey  = "config"
	IstioAppLabel                 = "app"
	ServiceProtocolUDP            = "UDP"
	initializer_disabled          = "configmaps \"" +
		IstioInitializerConfigMap + "\" not found"
	initializer_disabled_summary = "Istio initializer is not configured." +
		" Enable initializer and automatic sidecar injection to use "
	kubernetesServiceName = "kubernetes"
)

// Following types are taken from
// https://github.com/istio/istio/blob/master/pilot/platform/kube/inject/inject.go

type InjectionPolicy string

// Params describes configurable parameters for injecting istio proxy
// into kubernetes resource.
type Params struct {
	InitImage       string                  `json:"initImage"`
	ProxyImage      string                  `json:"proxyImage"`
	Verbosity       int                     `json:"verbosity"`
	SidecarProxyUID int64                   `json:"sidecarProxyUID"`
	Version         string                  `json:"version"`
	EnableCoreDump  bool                    `json:"enableCoreDump"`
	DebugMode       bool                    `json:"debugMode"`
	Mesh            *proxyconfig.MeshConfig `json:"-"`
	ImagePullPolicy string                  `json:"imagePullPolicy"`
	// Comma separated list of IP ranges in CIDR form. If set, only
	// redirect outbound traffic to Envoy for these IP
	// ranges. Otherwise all outbound traffic is redirected to Envoy.
	IncludeIPRanges string `json:"includeIPRanges"`
}

type IstioInjectConfig struct {
	Policy InjectionPolicy `json:"policy"`

	// deprecate if InitializerConfiguration becomes namespace aware
	IncludeNamespaces []string `json:"namespaces"`

	// deprecate if InitializerConfiguration becomes namespace aware
	ExcludeNamespaces []string `json:"excludeNamespaces"`

	// Params specifies the parameters of the injected sidcar template
	Params Params `json:"params"`

	// InitializerName specifies the name of the initializer.
	InitializerName string `json:"initializerName"`
}

var istioSupportedServicePrefix = []string{
	"http", "http-",
	"http2", "http2-",
	"grpc", "grpc-",
	"mongo", "mongo-",
	"redis", "redis-",
	"tcp", "tcp-"}

var defaultExemptedNamespaces = map[string]bool{
	"kube-system":  true,
	"kube-public":  true,
	"istio-system": true}

// DefaultExemptedNamespaces returns list of default Namsepaces which are
// exempted from automatic sidecar injection.
// List includes "kube-system", "kube-public" and "istio-system"
func DefaultExemptedNamespaces() []string {
	s := make([]string, len(defaultExemptedNamespaces))
	i := 0
	for k, _ := range defaultExemptedNamespaces {
		s[i] = k
		i++
	}
	return s
}

// ExemptedNamespace checks if a Namespace is by default exempted from automatic
// sidecar injection.
func ExemptedNamespace(ns string) bool {
	return defaultExemptedNamespaces[ns]
}

// GetInitializerConfig retrieves the Istio Initializer config.
// Istio Initializer config is stored as "istio-inject" configmap in
// "istio-system" Namespace.
func GetInitializerConfig(c kubernetes.Interface) (*IstioInjectConfig, error) {
	cm, err :=
		c.CoreV1().ConfigMaps(IstioNamespace).Get(IstioInitializerConfigMap, metav1.GetOptions{})
	if err != nil {
		glog.V(2).Infof("Failed to retrieve configmap: %s error: %s", IstioInitializerConfigMap, err)
		return nil, err
	}
	d, e := cm.Data[IstioInitializerConfigMapKey]
	if !e {
		errStr := fmt.Sprintf("Missing configuration map key: %s in configmap: %s", IstioInitializerConfigMapKey, IstioInitializerConfigMap)
		glog.Errorf(errStr)
		return nil, errors.New(errStr)
	}
	var cfg IstioInjectConfig
	if err := yaml.Unmarshal([]byte(d), &cfg); err != nil {
		glog.Errorf("Failed to parse yaml initializer config: %s", err)
		return nil, err
	}
	return &cfg, nil
}

// IstioInitializerDisabledNote generates an INFO note if the error string
// contains "istio-inject configmap not found".
func IstioInitializerDisabledNote(e, vetterId, vetterType string) *apiv1.Note {
	if strings.Contains(e, initializer_disabled) {
		return &apiv1.Note{
			Type:    vetterType,
			Summary: initializer_disabled_summary + "\"" + vetterId + "\" vetter.",
			Level:   apiv1.NoteLevel_INFO}
	}
	return nil
}

// ServicePortPrefixed checks if the Service port name is prefixed with Istio
// supported protocols.
func ServicePortPrefixed(n string) bool {
	i := 0
	for i < len(istioSupportedServicePrefix) {
		if n == istioSupportedServicePrefix[i] || strings.HasPrefix(n, istioSupportedServicePrefix[i+1]) {
			return true
		}
		i += 2
	}
	return false
}

// SidecarInjected checks if sidecar is injected in a Pod.
// Sidecar is considered injected if initializer annotation and proxy container
// are both present in the Pod Spec.
func SidecarInjected(p corev1.Pod) bool {
	if _, ok := p.Annotations[IstioInitializerPodAnnotation]; !ok {
		return false
	}
	cList := p.Spec.Containers
	for _, c := range cList {
		if c.Name == IstioProxyContainerName {
			return true
		}
	}
	return false
}

// ImageTag returns the Image tag of a named Container if present in the Pod Spec.
// If no version is specified "latest" is returned.
// Returns error if Container is not present in the Pod Spec.
func ImageTag(n string, s corev1.PodSpec) (string, error) {
	cList := s.Containers
	for _, c := range cList {
		if c.Name == n {
			imageTags := strings.Split(c.Image, ":")
			if len(imageTags) == 1 {
				return "latest", nil
			} else {
				return imageTags[len(imageTags)-1], nil
			}
		}
	}
	errStr := fmt.Sprintf("Failed to find container: %s", n)
	glog.Error(errStr)
	return "", errors.New(errStr)
}

func existsInStringSlice(e string, list []string) bool {
	for i := range list {
		if e == list[i] {
			return true
		}
	}
	return false
}

// ListNamespacesInMesh returns the list of Namespaces in the mesh.
// Inspects the Istio Initializer(istio-inject) configmap to enumerate
// Namespaces included/excluded from the mesh.
func ListNamespacesInMesh(c kubernetes.Interface) ([]corev1.Namespace, error) {
	opts := metav1.ListOptions{}
	namespaces := []corev1.Namespace{}
	ns, err := c.CoreV1().Namespaces().List(opts)
	if err != nil {
		glog.Error("Failed to retrieve namespaces: ", err)
		return nil, err
	}
	cfg, err := GetInitializerConfig(c)
	if err != nil {
		return nil, err
	}
	for _, n := range ns.Items {
		if ExemptedNamespace(n.Name) == true {
			continue
		}
		if cfg.ExcludeNamespaces != nil && len(cfg.ExcludeNamespaces) > 0 {
			excluded := existsInStringSlice(n.Name, cfg.ExcludeNamespaces)
			if excluded == true {
				continue
			}
		}
		if cfg.IncludeNamespaces != nil && len(cfg.IncludeNamespaces) > 0 {
			included := existsInStringSlice(corev1.NamespaceAll, cfg.IncludeNamespaces) ||
				existsInStringSlice(n.Name, cfg.IncludeNamespaces)
			if included == false {
				continue
			}
		}
		namespaces = append(namespaces, n)
	}
	return namespaces, nil
}

// ListPodsInMesh returns the list of Pods in the mesh.
// Pods in Namespaces returned by ListNamespacesInMesh with sidecar
// injected as determined by SidecarInjected are considered in the mesh.
func ListPodsInMesh(c kubernetes.Interface) ([]corev1.Pod, error) {
	opts := metav1.ListOptions{}
	pods := []corev1.Pod{}
	ns, err := ListNamespacesInMesh(c)
	if err != nil {
		return nil, err
	}
	for _, n := range ns {
		podList, err := c.CoreV1().Pods(n.Name).List(opts)
		if err != nil {
			glog.Errorf("Failed to retrieve pods for namespace: %s error: %s", n.Name, err)
			return nil, err
		}
		for _, p := range podList.Items {
			if SidecarInjected(p) == true {
				pods = append(pods, p)
			}
		}
	}
	return pods, nil
}

// ListServicesInMesh returns the list of Services in the mesh.
// Services in Namespaces returned by ListNamespacesInMesh are considered in the mesh.
func ListServicesInMesh(c kubernetes.Interface) ([]corev1.Service, error) {
	opts := metav1.ListOptions{}
	services := []corev1.Service{}
	ns, err := ListNamespacesInMesh(c)
	if err != nil {
		return nil, err
	}
	for _, n := range ns {
		serviceList, err := c.CoreV1().Services(n.Name).List(opts)
		if err != nil {
			glog.Errorf("Failed to retrieve services for namespace: %s error: %s", n.Name, err)
			return nil, err
		}
		for _, s := range serviceList.Items {
			if s.Name != "kubernetes" {
				services = append(services, s)
			}
		}
	}
	return services, nil
}

// ListEndpointsInMesh returns the list of Endpoints in the mesh.
// Endpoints in Namespaces returned by ListNamespacesInMesh are considered in the mesh.
func ListEndpointsInMesh(c kubernetes.Interface) ([]corev1.Endpoints, error) {
	opts := metav1.ListOptions{}
	endpoints := []corev1.Endpoints{}
	ns, err := ListNamespacesInMesh(c)
	if err != nil {
		return nil, err
	}
	for _, n := range ns {
		endpointList, err := c.CoreV1().Endpoints(n.Name).List(opts)
		if err != nil {
			glog.Errorf("Failed to retrieve endpoints for namespace: %s error: %s", n.Name, err)
			return nil, err
		}
		for _, s := range endpointList.Items {
			if s.Name != kubernetesServiceName {
				endpoints = append(endpoints, s)
			}
		}
	}
	return endpoints, nil
}

// ComputeId returns MD5 checksum of the Note struct which can be used as
// ID for the note.
func ComputeId(n *apiv1.Note) string {
	return fmt.Sprintf("%x", structhash.Md5(n, 1))
}
