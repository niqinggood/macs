import json

import pytest

pd = pytest.importorskip("pandas")
pytest.importorskip("akshare")

import stock_data


def test_percentile_rank_ignores_invalid_and_negative_pe_values():
    values = [None, float("nan"), -8, 5, 10, 15, 20]
    assert stock_data._percentile_rank(values, 10, positive_only=True) == 50.0
    assert stock_data._percentile_rank(values, -2, positive_only=True) is None


def test_technical_summary_contains_short_term_windows_and_finite_json():
    dates = pd.date_range("2026-01-01", periods=80, freq="B")
    close = pd.Series([10 + index * 0.05 for index in range(80)])
    frame = pd.DataFrame(
        {
            "日期": dates,
            "开盘": close - 0.03,
            "收盘": close,
            "最高": close + 0.10,
            "最低": close - 0.10,
            "成交量": [1000 + index * 10 for index in range(80)],
            "成交额": [10000 + index * 100 for index in range(80)],
            "换手率": [1.0 + index * 0.01 for index in range(80)],
        }
    )

    result = stock_data._technical_summary(frame)

    for key in ["return_1d_pct", "return_3d_pct", "return_5d_pct", "return_10d_pct", "return_20d_pct"]:
        assert result[key] is not None
    for key in ["ma5", "ma10", "ma20", "ma60", "macd_hist", "rsi14", "atr14_pct"]:
        assert result[key] is not None
    assert 0 <= result["range_20d"]["position_pct"] <= 100
    assert result["volume_vs_20d"] is not None
    json.dumps(result, allow_nan=False)


def test_market_code_normalization():
    assert stock_data._market_prefix("600596") == "sh"
    assert stock_data._market_prefix("000001") == "sz"
    assert stock_data._market_prefix("830799") == "bj"
    assert stock_data._eastmoney_code("600596") == "600596.SH"


def test_fund_flow_uses_ths_fallback_when_history_source_fails(monkeypatch):
    def fail_history(**_kwargs):
        raise RuntimeError("primary unavailable")

    def ths_snapshot(symbol):
        assert symbol == "即时"
        return pd.DataFrame(
            [{
                "股票代码": 600596,
                "股票简称": "新安股份",
                "最新价": 9.8,
                "涨跌幅": "1.2%",
                "换手率": "2.3%",
                "流入资金": "1.1亿",
                "流出资金": "0.9亿",
                "净额": "0.2亿",
            }]
        )

    monkeypatch.setattr(stock_data.ak, "stock_individual_fund_flow", fail_history)
    monkeypatch.setattr(stock_data.ak, "stock_fund_flow_individual", ths_snapshot)

    result = stock_data._fund_flow_summary("600596")

    assert result["status"] == "observed"
    assert result["source"].startswith("同花顺")
    assert list(result["periods"]) == ["即时"]
    assert "多日资金流" in result["limitations"]
