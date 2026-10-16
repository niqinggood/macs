package network

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWeatherForecastLocationFallbackHandlesQualifiedEnglishName(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.URL.Query().Get("name") == "Shanghai, China" {
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"results": []interface{}{}})
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"results": []map[string]interface{}{{
				"name": "Shanghai", "admin1": "Shanghai", "country": "China", "country_code": "CN",
				"timezone": "Asia/Shanghai", "latitude": 31.2304, "longitude": 121.4737,
			}},
		})
	}))
	defer server.Close()

	tool := NewWeatherForecastToolWithConfig(WeatherForecastConfig{
		Client:            server.Client(),
		GeocodingEndpoint: server.URL,
	})
	location, err := tool.resolveLocation(context.Background(), "Shanghai, China")
	if err != nil {
		t.Fatalf("resolveLocation() error = %v", err)
	}
	if calls != 2 || location.CountryCode != "CN" || location.Name != "Shanghai" {
		t.Fatalf("fallback calls/location = %d/%+v", calls, location)
	}
}
