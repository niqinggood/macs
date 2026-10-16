package app

import (
	"strings"
	"testing"
)

func TestSourceURLForStockFactsRoutesAshareMarkets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		symbol        string
		tencentSymbol string
		eastmoneyID   string
	}{
		{name: "Shanghai fund prefix", symbol: "510300", tencentSymbol: "sh510300", eastmoneyID: "1.510300"},
		{name: "Shanghai stock prefix", symbol: "600519", tencentSymbol: "sh600519", eastmoneyID: "1.600519"},
		{name: "Shenzhen stock prefix zero", symbol: "000001", tencentSymbol: "sz000001", eastmoneyID: "0.000001"},
		{name: "Shenzhen fund prefix one", symbol: "159915", tencentSymbol: "sz159915", eastmoneyID: "0.159915"},
		{name: "Shenzhen B-share prefix two", symbol: "200002", tencentSymbol: "sz200002", eastmoneyID: "0.200002"},
		{name: "Shenzhen ChiNext prefix three", symbol: "300750", tencentSymbol: "sz300750", eastmoneyID: "0.300750"},
		{name: "Qualified Shanghai Composite", symbol: "sh000001", tencentSymbol: "sh000001", eastmoneyID: "1.000001"},
		{name: "Qualified STAR 50", symbol: "sh000688", tencentSymbol: "sh000688", eastmoneyID: "1.000688"},
		{name: "Qualified Shenzhen Component", symbol: "sz399001", tencentSymbol: "sz399001", eastmoneyID: "0.399001"},
		{name: "Beijing legacy prefix four", symbol: "430047", tencentSymbol: "bj430047", eastmoneyID: "0.430047"},
		{name: "Beijing legacy prefix eight", symbol: "830799", tencentSymbol: "bj830799", eastmoneyID: "0.830799"},
		{name: "Beijing new prefix nine", symbol: "920002", tencentSymbol: "bj920002", eastmoneyID: "0.920002"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tencentURL := sourceURLForToolCall(ToolCall{
				Name: "stock_facts",
				Arguments: map[string]interface{}{
					"provider": "tencent_market",
					"symbol":   test.symbol,
				},
			})
			if want := "param=" + test.tencentSymbol + ",day,,,80,none"; !strings.Contains(tencentURL, want) {
				t.Fatalf("Tencent URL = %q, want it to contain %q", tencentURL, want)
			}

			eastmoneyURL := sourceURLForToolCall(ToolCall{
				Name: "stock_facts",
				Arguments: map[string]interface{}{
					"provider": "eastmoney_market",
					"symbol":   test.symbol,
				},
			})
			if !strings.Contains(eastmoneyURL, "https://push2delay.eastmoney.com/") {
				t.Fatalf("Eastmoney URL = %q, want the stable delayed snapshot host", eastmoneyURL)
			}
			if want := "secid=" + test.eastmoneyID; !strings.Contains(eastmoneyURL, want) {
				t.Fatalf("Eastmoney URL = %q, want it to contain %q", eastmoneyURL, want)
			}
		})
	}
}

func TestSourceURLForStockFactsTickerFallbackUsesBeijingRoute(t *testing.T) {
	t.Parallel()

	call := ToolCall{
		Name: "stock_facts",
		Arguments: map[string]interface{}{
			"provider": "tencent_market",
			"ticker":   " 920002 ",
		},
	}
	if got := sourceURLForToolCall(call); !strings.Contains(got, "param=bj920002,day,,,80,none") {
		t.Fatalf("Tencent ticker-fallback URL = %q, want Beijing route", got)
	}
}
