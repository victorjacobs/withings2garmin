package withings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchMeasurementsPaginates(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if request.Header.Get("Authorization") != "Bearer access" {
			t.Fatal("authorization missing")
		}
		if request.Form.Get("action") != "getmeas" ||
			request.Form.Get("meastype") != "1" ||
			request.Form.Get("category") != "1" {
			t.Fatalf("unexpected form %v", request.Form)
		}
		if requests == 1 {
			_, _ = response.Write([]byte(`{"status":0,"body":{` +
				`"updatetime":200,"more":1,"offset":42,"measuregrps":[{` +
				`"grpid":1,"category":1,"date":100,"created":100,"modified":101,` +
				`"attrib":0,"deviceid":"d","model":"m","timezone":"Europe/Brussels",` +
				`"measures":[{"type":1,"value":75420,"unit":-3}]` +
				`}]}}`))
			return
		}
		if request.Form.Get("offset") != "42" {
			t.Fatalf("offset=%q", request.Form.Get("offset"))
		}
		_, _ = response.Write([]byte(`{"status":0,"body":{` +
			`"updatetime":199,"more":0,"measuregrps":[{` +
			`"grpid":2,"category":1,"date":102,"created":102,"modified":102,` +
			`"attrib":8,"deviceid":9,"model":12,"measures":[{"type":1,"value":800,"unit":-1}]` +
			`}]}}`))
	}))
	defer server.Close()
	client := NewClient(server.Client())
	client.SetMeasureURL(server.URL)
	start, end := time.Unix(1, 0), time.Unix(2, 0)
	result, err := client.FetchMeasurements(context.Background(), "access", Query{StartDate: &start, EndDate: &end})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(result.Groups) != 2 || !result.UpdateTime.Equal(time.Unix(199, 0).UTC()) {
		t.Fatalf("unexpected result: %#v", result)
	}
	measurement, err := WeightFromGroup(result.Groups[0])
	if err != nil || measurement.WeightGrams != 75420 {
		t.Fatalf("measurement=%#v err=%v", measurement, err)
	}
}

func TestWeightConversionAndAttribution(t *testing.T) {
	grams, err := gramsFromValue(15, -4)
	if err != nil || grams != 2 {
		t.Fatalf("grams=%d err=%v", grams, err)
	}
	grams, err = gramsFromValue(14, -4)
	if err != nil || grams != 1 {
		t.Fatalf("grams=%d err=%v", grams, err)
	}
	if FilterAttribution(1, false) != AttributionAmbiguous ||
		FilterAttribution(1, true) != AttributionAccepted ||
		FilterAttribution(2, true) != AttributionManual {
		t.Fatal("unexpected attribution filtering")
	}
	group := MeasureGroup{
		GroupID:    1,
		Category:   1,
		MeasuredAt: time.Now(),
		Measures: []Measure{{
			Type:  1,
			Value: 1,
			Unit:  3,
		}},
	}
	if _, err := WeightFromGroup(group); err == nil {
		t.Fatal("expected impossible weight rejection")
	}
}

func TestFetchNoData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"status":100,"body":{"updatetime":123}}`))
	}))
	defer server.Close()
	client := NewClient(server.Client())
	client.SetMeasureURL(server.URL)
	result, err := client.FetchMeasurements(context.Background(), "access", Query{})
	if err != nil || !result.NoData || !result.UpdateTime.Equal(time.Unix(123, 0).UTC()) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
