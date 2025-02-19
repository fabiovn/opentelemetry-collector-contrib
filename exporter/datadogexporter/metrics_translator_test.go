// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package datadogexporter

import (
	"math"
	"testing"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/model/pdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gopkg.in/zorkian/go-datadog-api.v2"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/datadogexporter/config"
	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/datadogexporter/internal/metadata"
	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/datadogexporter/internal/metrics"
)

func TestMetricValue(t *testing.T) {
	var (
		name  = "name"
		value = math.Pi
		ts    = uint64(time.Now().UnixNano())
		tags  = []string{"tool:opentelemetry", "version:0.1.0"}
	)

	metric := metrics.NewGauge(name, ts, value, tags)
	assert.Equal(t, string(metrics.Gauge), metric.GetType())
	assert.Equal(t, tags, metric.Tags)
}

func TestGetTags(t *testing.T) {
	attributes := pdata.NewAttributeMap().InitFromMap(map[string]pdata.AttributeValue{
		"key1": pdata.NewAttributeValueString("val1"),
		"key2": pdata.NewAttributeValueString("val2"),
		"key3": pdata.NewAttributeValueString(""),
	})

	assert.ElementsMatch(t,
		getTags(attributes),
		[...]string{"key1:val1", "key2:val2", "key3:n/a"},
	)
}

func TestIsCumulativeMonotonic(t *testing.T) {
	// Some of these examples are from the hostmetrics receiver
	// and reflect the semantic meaning of the metrics there.
	//
	// If the receiver changes these examples should be added here too

	{ // Sum: Cumulative but not monotonic
		metric := pdata.NewMetric()
		metric.SetName("system.filesystem.usage")
		metric.SetDescription("Filesystem bytes used.")
		metric.SetUnit("bytes")
		metric.SetDataType(pdata.MetricDataTypeSum)
		sum := metric.Sum()
		sum.SetIsMonotonic(false)
		sum.SetAggregationTemporality(pdata.AggregationTemporalityCumulative)

		assert.False(t, isCumulativeMonotonic(metric))
	}

	{ // Sum: Cumulative and monotonic
		metric := pdata.NewMetric()
		metric.SetName("system.network.packets")
		metric.SetDescription("The number of packets transferred.")
		metric.SetUnit("1")
		metric.SetDataType(pdata.MetricDataTypeSum)
		sum := metric.Sum()
		sum.SetIsMonotonic(true)
		sum.SetAggregationTemporality(pdata.AggregationTemporalityCumulative)

		assert.True(t, isCumulativeMonotonic(metric))
	}

	{ // DoubleSumL Cumulative and monotonic
		metric := pdata.NewMetric()
		metric.SetName("metric.example")
		metric.SetDataType(pdata.MetricDataTypeSum)
		sum := metric.Sum()
		sum.SetIsMonotonic(true)
		sum.SetAggregationTemporality(pdata.AggregationTemporalityCumulative)

		assert.True(t, isCumulativeMonotonic(metric))
	}

	{ // Not IntSum
		metric := pdata.NewMetric()
		metric.SetName("system.cpu.load_average.1m")
		metric.SetDescription("Average CPU Load over 1 minute.")
		metric.SetUnit("1")
		metric.SetDataType(pdata.MetricDataTypeGauge)

		assert.False(t, isCumulativeMonotonic(metric))
	}
}

func TestMetricDimensionsToMapKey(t *testing.T) {
	metricName := "metric.name"
	noTags := metricDimensionsToMapKey(metricName, []string{})
	someTags := metricDimensionsToMapKey(metricName, []string{"key1:val1", "key2:val2"})
	sameTags := metricDimensionsToMapKey(metricName, []string{"key2:val2", "key1:val1"})
	diffTags := metricDimensionsToMapKey(metricName, []string{"key3:val3"})

	assert.NotEqual(t, noTags, someTags)
	assert.NotEqual(t, someTags, diffTags)
	assert.Equal(t, someTags, sameTags)
}

func TestMapIntMetrics(t *testing.T) {
	ts := pdata.TimestampFromTime(time.Now())
	slice := pdata.NewNumberDataPointSlice()
	point := slice.AppendEmpty()
	point.SetIntVal(17)
	point.SetTimestamp(ts)

	assert.ElementsMatch(t,
		mapNumberMetrics("int64.test", metrics.Gauge, slice, []string{}),
		[]datadog.Metric{metrics.NewGauge("int64.test", uint64(ts), 17, []string{})},
	)

	assert.ElementsMatch(t,
		mapNumberMetrics("int64.delta.test", metrics.Count, slice, []string{}),
		[]datadog.Metric{metrics.NewCount("int64.delta.test", uint64(ts), 17, []string{})},
	)

	// With attribute tags
	assert.ElementsMatch(t,
		mapNumberMetrics("int64.test", metrics.Gauge, slice, []string{"attribute_tag:attribute_value"}),
		[]datadog.Metric{metrics.NewGauge("int64.test", uint64(ts), 17, []string{"attribute_tag:attribute_value"})},
	)
}

func TestMapDoubleMetrics(t *testing.T) {
	ts := pdata.TimestampFromTime(time.Now())
	slice := pdata.NewNumberDataPointSlice()
	point := slice.AppendEmpty()
	point.SetDoubleVal(math.Pi)
	point.SetTimestamp(ts)

	assert.ElementsMatch(t,
		mapNumberMetrics("float64.test", metrics.Gauge, slice, []string{}),
		[]datadog.Metric{metrics.NewGauge("float64.test", uint64(ts), math.Pi, []string{})},
	)

	assert.ElementsMatch(t,
		mapNumberMetrics("float64.delta.test", metrics.Count, slice, []string{}),
		[]datadog.Metric{metrics.NewCount("float64.delta.test", uint64(ts), math.Pi, []string{})},
	)

	// With attribute tags
	assert.ElementsMatch(t,
		mapNumberMetrics("float64.test", metrics.Gauge, slice, []string{"attribute_tag:attribute_value"}),
		[]datadog.Metric{metrics.NewGauge("float64.test", uint64(ts), math.Pi, []string{"attribute_tag:attribute_value"})},
	)
}

func newTestCache() *ttlCache {
	cache := newTTLCache(1800, 3600)
	return cache
}

func seconds(i int) pdata.Timestamp {
	return pdata.TimestampFromTime(time.Unix(int64(i), 0))
}

func TestPutAndGetDiff(t *testing.T) {
	prevPts := newTestCache()
	_, ok := prevPts.putAndGetDiff("test", 1, 5)
	// no diff since it is the first point
	assert.False(t, ok)
	_, ok = prevPts.putAndGetDiff("test", 0, 0)
	// no diff since ts is lower than the stored point
	assert.False(t, ok)
	_, ok = prevPts.putAndGetDiff("test", 2, 2)
	// no diff since the value is lower than the stored value
	assert.False(t, ok)
	dx, ok := prevPts.putAndGetDiff("test", 3, 4)
	// diff with the most recent point (2,2)
	assert.True(t, ok)
	assert.Equal(t, 2.0, dx)
}

func TestMapIntMonotonicMetrics(t *testing.T) {
	// Create list of values
	deltas := []int64{1, 2, 200, 3, 7, 0}
	cumulative := make([]int64, len(deltas)+1)
	cumulative[0] = 0
	for i := 1; i < len(cumulative); i++ {
		cumulative[i] = cumulative[i-1] + deltas[i-1]
	}

	//Map to OpenTelemetry format
	slice := pdata.NewNumberDataPointSlice()
	slice.EnsureCapacity(len(cumulative))
	for i, val := range cumulative {
		point := slice.AppendEmpty()
		point.SetIntVal(val)
		point.SetTimestamp(seconds(i))
	}

	// Map to Datadog format
	metricName := "metric.example"
	expected := make([]datadog.Metric, len(deltas))
	for i, val := range deltas {
		expected[i] = metrics.NewCount(metricName, uint64(seconds(i+1)), float64(val), []string{})
	}

	prevPts := newTestCache()
	output := mapNumberMonotonicMetrics(metricName, prevPts, slice, []string{})

	assert.ElementsMatch(t, output, expected)
}

func TestMapIntMonotonicDifferentDimensions(t *testing.T) {
	metricName := "metric.example"
	slice := pdata.NewNumberDataPointSlice()

	// No tags
	point := slice.AppendEmpty()
	point.SetTimestamp(seconds(0))

	point = slice.AppendEmpty()
	point.SetIntVal(20)
	point.SetTimestamp(seconds(1))

	// One tag: valA
	point = slice.AppendEmpty()
	point.SetTimestamp(seconds(0))
	point.Attributes().InsertString("key1", "valA")

	point = slice.AppendEmpty()
	point.SetIntVal(30)
	point.SetTimestamp(seconds(1))
	point.Attributes().InsertString("key1", "valA")

	// same tag: valB
	point = slice.AppendEmpty()
	point.SetTimestamp(seconds(0))
	point.Attributes().InsertString("key1", "valB")

	point = slice.AppendEmpty()
	point.SetIntVal(40)
	point.SetTimestamp(seconds(1))
	point.Attributes().InsertString("key1", "valB")

	prevPts := newTestCache()

	assert.ElementsMatch(t,
		mapNumberMonotonicMetrics(metricName, prevPts, slice, []string{}),
		[]datadog.Metric{
			metrics.NewCount(metricName, uint64(seconds(1)), 20, []string{}),
			metrics.NewCount(metricName, uint64(seconds(1)), 30, []string{"key1:valA"}),
			metrics.NewCount(metricName, uint64(seconds(1)), 40, []string{"key1:valB"}),
		},
	)
}

func TestMapIntMonotonicWithReboot(t *testing.T) {
	values := []int64{0, 30, 0, 20}
	metricName := "metric.example"
	slice := pdata.NewNumberDataPointSlice()
	slice.EnsureCapacity(len(values))

	for i, val := range values {
		point := slice.AppendEmpty()
		point.SetTimestamp(seconds(i))
		point.SetIntVal(val)
	}

	prevPts := newTestCache()
	assert.ElementsMatch(t,
		mapNumberMonotonicMetrics(metricName, prevPts, slice, []string{}),
		[]datadog.Metric{
			metrics.NewCount(metricName, uint64(seconds(1)), 30, []string{}),
			metrics.NewCount(metricName, uint64(seconds(3)), 20, []string{}),
		},
	)
}

func TestMapIntMonotonicOutOfOrder(t *testing.T) {
	stamps := []int{1, 0, 2, 3}
	values := []int64{0, 1, 2, 3}

	metricName := "metric.example"
	slice := pdata.NewNumberDataPointSlice()
	slice.EnsureCapacity(len(values))

	for i, val := range values {
		point := slice.AppendEmpty()
		point.SetTimestamp(seconds(stamps[i]))
		point.SetIntVal(val)
	}

	prevPts := newTestCache()
	assert.ElementsMatch(t,
		mapNumberMonotonicMetrics(metricName, prevPts, slice, []string{}),
		[]datadog.Metric{
			metrics.NewCount(metricName, uint64(seconds(2)), 2, []string{}),
			metrics.NewCount(metricName, uint64(seconds(3)), 1, []string{}),
		},
	)
}

func TestMapDoubleMonotonicMetrics(t *testing.T) {
	deltas := []float64{1, 2, 200, 3, 7, 0}
	cumulative := make([]float64, len(deltas)+1)
	cumulative[0] = 0
	for i := 1; i < len(cumulative); i++ {
		cumulative[i] = cumulative[i-1] + deltas[i-1]
	}

	//Map to OpenTelemetry format
	slice := pdata.NewNumberDataPointSlice()
	slice.EnsureCapacity(len(cumulative))
	for i, val := range cumulative {
		point := slice.AppendEmpty()
		point.SetDoubleVal(val)
		point.SetTimestamp(seconds(i))
	}

	// Map to Datadog format
	metricName := "metric.example"
	expected := make([]datadog.Metric, len(deltas))
	for i, val := range deltas {
		expected[i] = metrics.NewCount(metricName, uint64(seconds(i+1)), val, []string{})
	}

	prevPts := newTestCache()
	output := mapNumberMonotonicMetrics(metricName, prevPts, slice, []string{})

	assert.ElementsMatch(t, expected, output)
}

func TestMapDoubleMonotonicDifferentDimensions(t *testing.T) {
	metricName := "metric.example"
	slice := pdata.NewNumberDataPointSlice()

	// No tags
	point := slice.AppendEmpty()
	point.SetTimestamp(seconds(0))

	point = slice.AppendEmpty()
	point.SetDoubleVal(20)
	point.SetTimestamp(seconds(1))

	// One tag: valA
	point = slice.AppendEmpty()
	point.SetTimestamp(seconds(0))
	point.Attributes().InsertString("key1", "valA")

	point = slice.AppendEmpty()
	point.SetDoubleVal(30)
	point.SetTimestamp(seconds(1))
	point.Attributes().InsertString("key1", "valA")

	// one tag: valB
	point = slice.AppendEmpty()
	point.SetTimestamp(seconds(0))
	point.Attributes().InsertString("key1", "valB")

	point = slice.AppendEmpty()
	point.SetDoubleVal(40)
	point.SetTimestamp(seconds(1))
	point.Attributes().InsertString("key1", "valB")

	prevPts := newTestCache()

	assert.ElementsMatch(t,
		mapNumberMonotonicMetrics(metricName, prevPts, slice, []string{}),
		[]datadog.Metric{
			metrics.NewCount(metricName, uint64(seconds(1)), 20, []string{}),
			metrics.NewCount(metricName, uint64(seconds(1)), 30, []string{"key1:valA"}),
			metrics.NewCount(metricName, uint64(seconds(1)), 40, []string{"key1:valB"}),
		},
	)
}

func TestMapDoubleMonotonicWithReboot(t *testing.T) {
	values := []float64{0, 30, 0, 20}
	metricName := "metric.example"
	slice := pdata.NewNumberDataPointSlice()
	slice.EnsureCapacity(len(values))

	for i, val := range values {
		point := slice.AppendEmpty()
		point.SetTimestamp(seconds(2 * i))
		point.SetDoubleVal(val)
	}

	prevPts := newTestCache()
	assert.ElementsMatch(t,
		mapNumberMonotonicMetrics(metricName, prevPts, slice, []string{}),
		[]datadog.Metric{
			metrics.NewCount(metricName, uint64(seconds(2)), 30, []string{}),
			metrics.NewCount(metricName, uint64(seconds(6)), 20, []string{}),
		},
	)
}

func TestMapDoubleMonotonicOutOfOrder(t *testing.T) {
	stamps := []int{1, 0, 2, 3}
	values := []float64{0, 1, 2, 3}

	metricName := "metric.example"
	slice := pdata.NewNumberDataPointSlice()
	slice.EnsureCapacity(len(values))

	for i, val := range values {
		point := slice.AppendEmpty()
		point.SetTimestamp(seconds(stamps[i]))
		point.SetDoubleVal(val)
	}

	prevPts := newTestCache()
	assert.ElementsMatch(t,
		mapNumberMonotonicMetrics(metricName, prevPts, slice, []string{}),
		[]datadog.Metric{
			metrics.NewCount(metricName, uint64(seconds(2)), 2, []string{}),
			metrics.NewCount(metricName, uint64(seconds(3)), 1, []string{}),
		},
	)
}

func TestMapHistogramMetrics(t *testing.T) {
	ts := pdata.TimestampFromTime(time.Now())
	slice := pdata.NewHistogramDataPointSlice()
	point := slice.AppendEmpty()
	point.SetCount(20)
	point.SetSum(math.Pi)
	point.SetBucketCounts([]uint64{2, 18})
	point.SetTimestamp(ts)

	noBuckets := []datadog.Metric{
		metrics.NewGauge("doubleHist.test.count", uint64(ts), 20, []string{}),
		metrics.NewGauge("doubleHist.test.sum", uint64(ts), math.Pi, []string{}),
	}

	buckets := []datadog.Metric{
		metrics.NewGauge("doubleHist.test.count_per_bucket", uint64(ts), 2, []string{"bucket_idx:0"}),
		metrics.NewGauge("doubleHist.test.count_per_bucket", uint64(ts), 18, []string{"bucket_idx:1"}),
	}

	assert.ElementsMatch(t,
		mapHistogramMetrics("doubleHist.test", slice, false, []string{}), // No buckets
		noBuckets,
	)

	assert.ElementsMatch(t,
		mapHistogramMetrics("doubleHist.test", slice, true, []string{}), // buckets
		append(noBuckets, buckets...),
	)

	// With attribute tags
	noBucketsAttributeTags := []datadog.Metric{
		metrics.NewGauge("doubleHist.test.count", uint64(ts), 20, []string{"attribute_tag:attribute_value"}),
		metrics.NewGauge("doubleHist.test.sum", uint64(ts), math.Pi, []string{"attribute_tag:attribute_value"}),
	}

	bucketsAttributeTags := []datadog.Metric{
		metrics.NewGauge("doubleHist.test.count_per_bucket", uint64(ts), 2, []string{"attribute_tag:attribute_value", "bucket_idx:0"}),
		metrics.NewGauge("doubleHist.test.count_per_bucket", uint64(ts), 18, []string{"attribute_tag:attribute_value", "bucket_idx:1"}),
	}

	assert.ElementsMatch(t,
		mapHistogramMetrics("doubleHist.test", slice, false, []string{"attribute_tag:attribute_value"}), // No buckets
		noBucketsAttributeTags,
	)

	assert.ElementsMatch(t,
		mapHistogramMetrics("doubleHist.test", slice, true, []string{"attribute_tag:attribute_value"}), // buckets
		append(noBucketsAttributeTags, bucketsAttributeTags...),
	)
}

func TestQuantileTag(t *testing.T) {
	tests := []struct {
		quantile float64
		tag      string
	}{
		{quantile: 0, tag: "quantile:0"},
		{quantile: 0.001, tag: "quantile:0.001"},
		{quantile: 0.9, tag: "quantile:0.9"},
		{quantile: 0.95, tag: "quantile:0.95"},
		{quantile: 0.99, tag: "quantile:0.99"},
		{quantile: 0.999, tag: "quantile:0.999"},
		{quantile: 1, tag: "quantile:1.0"},
		{quantile: 1e-10, tag: "quantile:1e-10"},
	}

	for _, test := range tests {
		assert.Equal(t, test.tag, getQuantileTag(test.quantile))
	}
}

func exampleSummaryDataPointSlice(ts pdata.Timestamp, sum float64, count uint64) pdata.SummaryDataPointSlice {
	slice := pdata.NewSummaryDataPointSlice()
	point := slice.AppendEmpty()
	point.SetCount(count)
	point.SetSum(sum)
	qSlice := point.QuantileValues()

	qMin := qSlice.AppendEmpty()
	qMin.SetQuantile(0.0)
	qMin.SetValue(0)

	qMedian := qSlice.AppendEmpty()
	qMedian.SetQuantile(0.5)
	qMedian.SetValue(100)

	q999 := qSlice.AppendEmpty()
	q999.SetQuantile(0.999)
	q999.SetValue(500)

	qMax := qSlice.AppendEmpty()
	qMax.SetQuantile(1)
	qMax.SetValue(600)
	point.SetTimestamp(ts)
	return slice
}

func TestMapSummaryMetrics(t *testing.T) {
	ts := pdata.TimestampFromTime(time.Now())
	slice := exampleSummaryDataPointSlice(ts, 10_001, 101)

	newPrevPts := func(tags []string) *ttlCache {
		prevPts := newTestCache()
		prevPts.cache.Set(metricDimensionsToMapKey("summary.example.count", tags), numberCounter{0, 1}, gocache.NoExpiration)
		prevPts.cache.Set(metricDimensionsToMapKey("summary.example.sum", tags), numberCounter{0, 1}, gocache.NoExpiration)
		return prevPts
	}

	noQuantiles := []datadog.Metric{
		metrics.NewCount("summary.example.count", uint64(ts), 100, []string{}),
		metrics.NewCount("summary.example.sum", uint64(ts), 10_000, []string{}),
	}
	quantiles := []datadog.Metric{
		metrics.NewGauge("summary.example.quantile", uint64(ts), 0, []string{"quantile:0"}),
		metrics.NewGauge("summary.example.quantile", uint64(ts), 100, []string{"quantile:0.5"}),
		metrics.NewGauge("summary.example.quantile", uint64(ts), 500, []string{"quantile:0.999"}),
		metrics.NewGauge("summary.example.quantile", uint64(ts), 600, []string{"quantile:1.0"}),
	}
	prevPts := newPrevPts([]string{})
	assert.ElementsMatch(t,
		mapSummaryMetrics("summary.example", prevPts, slice, false, []string{}),
		noQuantiles,
	)
	prevPts = newPrevPts([]string{})
	assert.ElementsMatch(t,
		mapSummaryMetrics("summary.example", prevPts, slice, true, []string{}),
		append(noQuantiles, quantiles...),
	)

	noQuantilesAttr := []datadog.Metric{
		metrics.NewCount("summary.example.count", uint64(ts), 100, []string{"attribute_tag:attribute_value"}),
		metrics.NewCount("summary.example.sum", uint64(ts), 10_000, []string{"attribute_tag:attribute_value"}),
	}
	quantilesAttr := []datadog.Metric{
		metrics.NewGauge("summary.example.quantile", uint64(ts), 0, []string{"attribute_tag:attribute_value", "quantile:0"}),
		metrics.NewGauge("summary.example.quantile", uint64(ts), 100, []string{"attribute_tag:attribute_value", "quantile:0.5"}),
		metrics.NewGauge("summary.example.quantile", uint64(ts), 500, []string{"attribute_tag:attribute_value", "quantile:0.999"}),
		metrics.NewGauge("summary.example.quantile", uint64(ts), 600, []string{"attribute_tag:attribute_value", "quantile:1.0"}),
	}
	prevPts = newPrevPts([]string{"attribute_tag:attribute_value"})
	assert.ElementsMatch(t,
		mapSummaryMetrics("summary.example", prevPts, slice, false, []string{"attribute_tag:attribute_value"}),
		noQuantilesAttr,
	)
	prevPts = newPrevPts([]string{"attribute_tag:attribute_value"})
	assert.ElementsMatch(t,
		mapSummaryMetrics("summary.example", prevPts, slice, true, []string{"attribute_tag:attribute_value"}),
		append(noQuantilesAttr, quantilesAttr...),
	)
}

func TestRunningMetrics(t *testing.T) {
	ms := pdata.NewMetrics()
	rms := ms.ResourceMetrics()

	rm := rms.AppendEmpty()
	resAttrs := rm.Resource().Attributes()
	resAttrs.Insert(metadata.AttributeDatadogHostname, pdata.NewAttributeValueString("resource-hostname-1"))

	rm = rms.AppendEmpty()
	resAttrs = rm.Resource().Attributes()
	resAttrs.Insert(metadata.AttributeDatadogHostname, pdata.NewAttributeValueString("resource-hostname-1"))

	rm = rms.AppendEmpty()
	resAttrs = rm.Resource().Attributes()
	resAttrs.Insert(metadata.AttributeDatadogHostname, pdata.NewAttributeValueString("resource-hostname-2"))

	rms.AppendEmpty()

	cfg := config.MetricsConfig{}
	prevPts := newTestCache()

	buildInfo := component.BuildInfo{
		Version: "1.0",
	}

	series, _ := mapMetrics(zap.NewNop(), cfg, prevPts, "fallbackHostname", ms, buildInfo)

	runningHostnames := []string{}

	for _, metric := range series {
		if *metric.Metric == "otel.datadog_exporter.metrics.running" {
			if metric.Host != nil {
				runningHostnames = append(runningHostnames, *metric.Host)
			}
		}
	}

	assert.ElementsMatch(t,
		runningHostnames,
		[]string{"fallbackHostname", "resource-hostname-1", "resource-hostname-2"},
	)

}

const (
	testHostname = "res-hostname"
)

func createTestMetrics() pdata.Metrics {
	md := pdata.NewMetrics()
	rms := md.ResourceMetrics()
	rm := rms.AppendEmpty()

	attrs := rm.Resource().Attributes()
	attrs.InsertString(metadata.AttributeDatadogHostname, testHostname)
	ilms := rm.InstrumentationLibraryMetrics()

	metricsArray := ilms.AppendEmpty().Metrics()
	metricsArray.AppendEmpty() // first one is TypeNone to test that it's ignored

	// IntGauge
	met := metricsArray.AppendEmpty()
	met.SetName("int.gauge")
	met.SetDataType(pdata.MetricDataTypeGauge)
	dpsInt := met.Gauge().DataPoints()
	dpInt := dpsInt.AppendEmpty()
	dpInt.SetTimestamp(seconds(0))
	dpInt.SetIntVal(1)

	// DoubleGauge
	met = metricsArray.AppendEmpty()
	met.SetName("double.gauge")
	met.SetDataType(pdata.MetricDataTypeGauge)
	dpsDouble := met.Gauge().DataPoints()
	dpDouble := dpsDouble.AppendEmpty()
	dpDouble.SetTimestamp(seconds(0))
	dpDouble.SetDoubleVal(math.Pi)

	// aggregation unspecified sum
	met = metricsArray.AppendEmpty()
	met.SetName("unspecified.sum")
	met.SetDataType(pdata.MetricDataTypeSum)
	met.Sum().SetAggregationTemporality(pdata.AggregationTemporalityUnspecified)

	// Int Sum (delta)
	met = metricsArray.AppendEmpty()
	met.SetName("int.delta.sum")
	met.SetDataType(pdata.MetricDataTypeSum)
	met.Sum().SetAggregationTemporality(pdata.AggregationTemporalityDelta)
	dpsInt = met.Sum().DataPoints()
	dpInt = dpsInt.AppendEmpty()
	dpInt.SetTimestamp(seconds(0))
	dpInt.SetIntVal(2)

	// Double Sum (delta)
	met = metricsArray.AppendEmpty()
	met.SetName("double.delta.sum")
	met.SetDataType(pdata.MetricDataTypeSum)
	met.Sum().SetAggregationTemporality(pdata.AggregationTemporalityDelta)
	dpsDouble = met.Sum().DataPoints()
	dpDouble = dpsDouble.AppendEmpty()
	dpDouble.SetTimestamp(seconds(0))
	dpDouble.SetDoubleVal(math.E)

	// Int Sum (delta monotonic)
	met = metricsArray.AppendEmpty()
	met.SetName("int.delta.monotonic.sum")
	met.SetDataType(pdata.MetricDataTypeSum)
	met.Sum().SetAggregationTemporality(pdata.AggregationTemporalityDelta)
	dpsInt = met.Sum().DataPoints()
	dpInt = dpsInt.AppendEmpty()
	dpInt.SetTimestamp(seconds(0))
	dpInt.SetIntVal(2)

	// Double Sum (delta monotonic)
	met = metricsArray.AppendEmpty()
	met.SetName("double.delta.monotonic.sum")
	met.SetDataType(pdata.MetricDataTypeSum)
	met.Sum().SetAggregationTemporality(pdata.AggregationTemporalityDelta)
	dpsDouble = met.Sum().DataPoints()
	dpDouble = dpsDouble.AppendEmpty()
	dpDouble.SetTimestamp(seconds(0))
	dpDouble.SetDoubleVal(math.E)

	// Histogram
	met = metricsArray.AppendEmpty()
	met.SetName("double.histogram")
	met.SetDataType(pdata.MetricDataTypeHistogram)
	dpsDoubleHist := met.Histogram().DataPoints()
	dpDoubleHist := dpsDoubleHist.AppendEmpty()
	dpDoubleHist.SetCount(20)
	dpDoubleHist.SetSum(math.Phi)
	dpDoubleHist.SetBucketCounts([]uint64{2, 18})
	dpDoubleHist.SetTimestamp(seconds(0))

	// Int Sum (cumulative)
	met = metricsArray.AppendEmpty()
	met.SetName("int.cumulative.sum")
	met.SetDataType(pdata.MetricDataTypeSum)
	met.Sum().SetAggregationTemporality(pdata.AggregationTemporalityCumulative)
	dpsInt = met.Sum().DataPoints()
	dpsInt.EnsureCapacity(2)
	dpInt = dpsInt.AppendEmpty()
	dpInt.SetTimestamp(seconds(0))
	dpInt.SetIntVal(4)

	// Double Sum (cumulative)
	met = metricsArray.AppendEmpty()
	met.SetName("double.cumulative.sum")
	met.SetDataType(pdata.MetricDataTypeSum)
	met.Sum().SetAggregationTemporality(pdata.AggregationTemporalityCumulative)
	dpsDouble = met.Sum().DataPoints()
	dpsDouble.EnsureCapacity(2)
	dpDouble = dpsDouble.AppendEmpty()
	dpDouble.SetTimestamp(seconds(0))
	dpDouble.SetDoubleVal(4)

	// Int Sum (cumulative monotonic)
	met = metricsArray.AppendEmpty()
	met.SetName("int.cumulative.monotonic.sum")
	met.SetDataType(pdata.MetricDataTypeSum)
	met.Sum().SetAggregationTemporality(pdata.AggregationTemporalityCumulative)
	met.Sum().SetIsMonotonic(true)
	dpsInt = met.Sum().DataPoints()
	dpsInt.EnsureCapacity(2)
	dpInt = dpsInt.AppendEmpty()
	dpInt.SetTimestamp(seconds(0))
	dpInt.SetIntVal(4)
	dpInt = dpsInt.AppendEmpty()
	dpInt.SetTimestamp(seconds(2))
	dpInt.SetIntVal(7)

	// Double Sum (cumulative monotonic)
	met = metricsArray.AppendEmpty()
	met.SetName("double.cumulative.monotonic.sum")
	met.SetDataType(pdata.MetricDataTypeSum)
	met.Sum().SetAggregationTemporality(pdata.AggregationTemporalityCumulative)
	met.Sum().SetIsMonotonic(true)
	dpsDouble = met.Sum().DataPoints()
	dpsDouble.EnsureCapacity(2)
	dpDouble = dpsDouble.AppendEmpty()
	dpDouble.SetTimestamp(seconds(0))
	dpDouble.SetDoubleVal(4)
	dpDouble = dpsDouble.AppendEmpty()
	dpDouble.SetTimestamp(seconds(2))
	dpDouble.SetDoubleVal(4 + math.Pi)

	// Summary
	met = metricsArray.AppendEmpty()
	met.SetName("summary")
	met.SetDataType(pdata.MetricDataTypeSummary)
	slice := exampleSummaryDataPointSlice(seconds(0), 1, 1)
	slice.CopyTo(met.Summary().DataPoints())

	met = metricsArray.AppendEmpty()
	met.SetName("summary")
	met.SetDataType(pdata.MetricDataTypeSummary)
	slice = exampleSummaryDataPointSlice(seconds(2), 10_001, 101)
	slice.CopyTo(met.Summary().DataPoints())
	return md
}

func removeRunningMetrics(series []datadog.Metric) []datadog.Metric {
	filtered := []datadog.Metric{}
	for _, m := range series {
		if m.GetMetric() != "otel.datadog_exporter.metrics.running" {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

func testGauge(name string, val float64) datadog.Metric {
	m := metrics.NewGauge(name, 0, val, []string{})
	m.SetHost(testHostname)
	return m
}

func testCount(name string, val float64, seconds uint64) datadog.Metric {
	m := metrics.NewCount(name, seconds*1e9, val, []string{})
	m.SetHost(testHostname)
	return m
}

func TestMapMetrics(t *testing.T) {
	md := createTestMetrics()
	cfg := config.MetricsConfig{SendMonotonic: true}
	buildInfo := component.BuildInfo{
		Version: "1.0",
	}

	core, observed := observer.New(zapcore.DebugLevel)
	testLogger := zap.New(core)
	series, dropped := mapMetrics(testLogger, cfg, newTestCache(), "", md, buildInfo)

	assert.Equal(t, dropped, 0)
	filtered := removeRunningMetrics(series)
	assert.ElementsMatch(t, filtered, []datadog.Metric{
		testGauge("int.gauge", 1),
		testGauge("double.gauge", math.Pi),
		testCount("int.delta.sum", 2, 0),
		testCount("double.delta.sum", math.E, 0),
		testCount("int.delta.monotonic.sum", 2, 0),
		testCount("double.delta.monotonic.sum", math.E, 0),
		testGauge("double.histogram.sum", math.Phi),
		testGauge("double.histogram.count", 20),
		testCount("summary.sum", 10_000, 2),
		testCount("summary.count", 100, 2),
		testGauge("int.cumulative.sum", 4),
		testGauge("double.cumulative.sum", 4),
		testCount("int.cumulative.monotonic.sum", 3, 2),
		testCount("double.cumulative.monotonic.sum", math.Pi, 2),
	})

	// One metric type was unknown or unsupported
	assert.Equal(t, observed.FilterMessage("Unknown or unsupported metric type").Len(), 1)
	// One metric aggregation temporality was unknown or unsupported
	assert.Equal(t, observed.FilterMessage("Unknown or unsupported aggregation temporality").Len(), 1)
}
