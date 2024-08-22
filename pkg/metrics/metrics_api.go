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

package metrics

import (
	"time"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

// CSIMetrics contains the metrics for Talos API calls.
type CSIMetrics struct {
	Duration *metrics.HistogramVec
	Errors   *metrics.CounterVec
}

var apiMetrics = registerAPIMetrics()

// ObserveRequest records the request latency and counts the errors.
func (mc *MetricContext) ObserveRequest(err error) error {
	apiMetrics.Duration.WithLabelValues(mc.attributes...).Observe(
		time.Since(mc.start).Seconds())

	if err != nil {
		apiMetrics.Errors.WithLabelValues(mc.attributes...).Inc()
	}

	return err
}

func registerAPIMetrics() *CSIMetrics {
	metrics := &CSIMetrics{
		Duration: metrics.NewHistogramVec(
			&metrics.HistogramOpts{
				Name:    "proxmox_api_request_duration_seconds",
				Help:    "Latency of an Proxmox API call",
				Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 30},
			}, []string{"request"}),
		Errors: metrics.NewCounterVec(
			&metrics.CounterOpts{
				Name: "proxmox_api_request_errors_total",
				Help: "Total number of errors for an Proxmox API call",
			}, []string{"request"}),
	}

	legacyregistry.MustRegister(
		metrics.Duration,
		metrics.Errors,
	)

	return metrics
}
