#!/usr/bin/env python3
"""AKShare 股票数据获取脚本
支持A股、港股、美股的历史K线、实时行情、财务数据等"""
import sys
import json
import argparse
import contextlib
import io
import math
from datetime import datetime, timedelta

import pandas as pd
import akshare as ak


def _to_float(value):
    """Convert provider values to finite floats without leaking NaN to JSON."""
    try:
        number = float(value)
    except (TypeError, ValueError):
        return None
    return number if math.isfinite(number) else None


def _json_safe(value):
    if isinstance(value, dict):
        return {str(key): _json_safe(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_json_safe(item) for item in value]
    if isinstance(value, (pd.Timestamp, datetime)):
        return value.isoformat()
    if hasattr(value, "item"):
        try:
            return _json_safe(value.item())
        except (TypeError, ValueError):
            pass
    try:
        if pd.isna(value):
            return None
    except (TypeError, ValueError):
        pass
    return value


def _percentile_rank(values, current, positive_only=False):
    current = _to_float(current)
    if current is None or (positive_only and current <= 0):
        return None
    clean = []
    for value in values:
        number = _to_float(value)
        if number is None or (positive_only and number <= 0):
            continue
        clean.append(number)
    if not clean:
        return None
    return round(sum(value <= current for value in clean) * 100.0 / len(clean), 2)


def _market_prefix(symbol):
    symbol = str(symbol).strip().zfill(6)
    if symbol.startswith(("5", "6", "9")):
        return "sh"
    if symbol.startswith(("4", "8")):
        return "bj"
    return "sz"


def _eastmoney_code(symbol):
    suffix = _market_prefix(symbol).upper()
    return f"{str(symbol).strip().zfill(6)}.{suffix}"


def _find_spot_row(symbol):
    symbol = str(symbol).strip().zfill(6)
    frame = ak.stock_zh_a_spot_em()
    if "代码" not in frame.columns:
        raise ValueError("AKShare spot response has no 代码 column")
    matches = frame[frame["代码"].astype(str).str.zfill(6) == symbol]
    if matches.empty:
        raise ValueError(f"symbol {symbol} not found in A-share spot data")
    return matches.iloc[0].to_dict()

def get_stock_hist(symbol, period="daily", start_date=None, end_date=None, adjust=""):
    """获取股票历史K线数据"""
    try:
        df = ak.stock_zh_a_hist(
            symbol=symbol,
            period=period,
            start_date=start_date,
            end_date=end_date,
            adjust=adjust
        )
        return {
            "symbol": symbol,
            "period": period,
            "adjust": adjust,
            "data": df.to_dict('records'),
            "count": len(df)
        }
    except Exception as e:
        return {"error": f"获取历史数据失败: {str(e)}"}

def get_stock_hist_min(symbol, period="5", start_date=None, end_date=None, adjust=""):
    """获取股票分钟K线数据"""
    try:
        df = ak.stock_zh_a_hist_min_em(
            symbol=symbol,
            period=period,
            start_date=start_date,
            end_date=end_date,
            adjust=adjust
        )
        return {
            "symbol": symbol,
            "period": f"{period}min",
            "adjust": adjust,
            "data": df.to_dict('records'),
            "count": len(df)
        }
    except Exception as e:
        return {"error": f"获取分钟数据失败: {str(e)}"}

def get_stock_spot():
    """获取全部A股实时行情"""
    try:
        df = ak.stock_zh_a_spot_em()
        return {
            "data": df.to_dict('records'),
            "count": len(df),
            "update_time": pd.Timestamp.now().strftime("%Y-%m-%d %H:%M:%S")
        }
    except Exception as e:
        return {"error": f"获取实时行情失败: {str(e)}"}

def get_stock_info(symbol):
    """获取个股基本信息"""
    try:
        df = ak.stock_individual_info_em(symbol=symbol)
        return {
            "symbol": symbol,
            "info": df.to_dict('records')[0] if len(df) > 0 else {}
        }
    except Exception as e:
        return {"error": f"获取个股信息失败: {str(e)}"}

def get_stock_fundamental(symbol):
    """获取股票基本面数据"""
    try:
        df = ak.stock_fundamental(symbol=symbol)
        return {
            "symbol": symbol,
            "fundamental": df.to_dict('records')
        }
    except Exception as e:
        return {"error": f"获取基本面数据失败: {str(e)}"}

def get_stock_valuation(symbol, years=5):
    """Get current and historical A-share valuation with explicit data status."""
    symbol = str(symbol).strip().zfill(6)
    fetched_at = datetime.now().astimezone().isoformat(timespec="seconds")
    result = {
        "symbol": symbol,
        "fetched_at": fetched_at,
        "current": {},
        "history": {"window_years": years},
        "sources": [],
        "errors": [],
    }

    try:
        row = _find_spot_row(symbol)
        pe_dynamic = _to_float(row.get("市盈率-动态"))
        pb = _to_float(row.get("市净率"))
        result["current"] = {
            "name": row.get("名称"),
            "price": _to_float(row.get("最新价")),
            "change_pct": _to_float(row.get("涨跌幅")),
            "turnover_rate_pct": _to_float(row.get("换手率")),
            "volume_ratio": _to_float(row.get("量比")),
            "pe_dynamic": pe_dynamic,
            "pe_dynamic_status": "observed" if pe_dynamic is not None and pe_dynamic > 0 else "not_applicable",
            "pb_mrq": pb,
            "pb_mrq_status": "observed" if pb is not None and pb > 0 else "not_applicable",
            "total_market_cap": _to_float(row.get("总市值")),
            "float_market_cap": _to_float(row.get("流通市值")),
        }
        result["sources"].append({
            "name": "东方财富-A股实时行情（AKShare）",
            "as_of": fetched_at,
            "fields": ["price", "pe_dynamic", "pb_mrq", "market_cap", "turnover_rate", "volume_ratio"],
        })
    except Exception as exc:
        result["errors"].append({"source": "akshare.stock_zh_a_spot_em", "error": str(exc)})

    login_result = None
    try:
        import baostock as bs

        if _market_prefix(symbol) == "bj":
            raise ValueError("Baostock historical valuation does not support Beijing symbols")
        with contextlib.redirect_stdout(io.StringIO()):
            login_result = bs.login()
        if login_result.error_code != "0":
            raise RuntimeError(login_result.error_msg)
        end = datetime.now().date()
        start = end - timedelta(days=max(1, years) * 366)
        query = bs.query_history_k_data_plus(
            f"{_market_prefix(symbol)}.{symbol}",
            "date,code,close,peTTM,pbMRQ,psTTM,pcfNcfTTM",
            start_date=start.isoformat(),
            end_date=end.isoformat(),
            frequency="d",
            adjustflag="3",
        )
        if query.error_code != "0":
            raise RuntimeError(query.error_msg)
        rows = []
        while query.next():
            rows.append(query.get_row_data())
        frame = pd.DataFrame(rows, columns=query.fields)
        if frame.empty:
            raise ValueError("Baostock returned no valuation rows")
        for column in ["close", "peTTM", "pbMRQ", "psTTM", "pcfNcfTTM"]:
            frame[column] = pd.to_numeric(frame[column], errors="coerce")
        frame = frame.sort_values("date")
        latest = frame.iloc[-1]
        pe_ttm = _to_float(latest.get("peTTM"))
        pb_mrq = _to_float(latest.get("pbMRQ"))
        ps_ttm = _to_float(latest.get("psTTM"))
        pcf_ttm = _to_float(latest.get("pcfNcfTTM"))
        result["as_of"] = str(latest.get("date"))
        result["history"].update({
            "observations": int(len(frame)),
            "pe_ttm": pe_ttm,
            "pe_ttm_status": "observed" if pe_ttm is not None and pe_ttm > 0 else "not_applicable",
            "pe_ttm_percentile": _percentile_rank(frame["peTTM"], pe_ttm, positive_only=True),
            "pb_mrq": pb_mrq,
            "pb_mrq_status": "observed" if pb_mrq is not None and pb_mrq > 0 else "not_applicable",
            "pb_mrq_percentile": _percentile_rank(frame["pbMRQ"], pb_mrq, positive_only=True),
            "ps_ttm": ps_ttm,
            "ps_ttm_status": "observed" if ps_ttm is not None and ps_ttm > 0 else "not_applicable",
            "ps_ttm_percentile": _percentile_rank(frame["psTTM"], ps_ttm, positive_only=True),
            "pcf_ncf_ttm": pcf_ttm,
            "pcf_ncf_ttm_status": "observed" if pcf_ttm is not None and pcf_ttm > 0 else "not_applicable",
        })
        if not result["current"]:
            result["current"] = {
                "price": _to_float(latest.get("close")),
                "price_status": "observed",
                "pe_ttm": pe_ttm,
                "pe_ttm_status": result["history"]["pe_ttm_status"],
                "pb_mrq": pb_mrq,
                "pb_mrq_status": result["history"]["pb_mrq_status"],
                "ps_ttm": ps_ttm,
                "ps_ttm_status": result["history"]["ps_ttm_status"],
                "snapshot_type": "latest_trading_day",
                "as_of": result["as_of"],
            }
        result["sources"].append({
            "name": "Baostock A股日频估值",
            "as_of": result["as_of"],
            "fields": ["peTTM", "pbMRQ", "psTTM", "pcfNcfTTM", "historical_percentile"],
        })
    except Exception as exc:
        result["errors"].append({"source": "baostock.query_history_k_data_plus", "error": str(exc)})
    finally:
        if login_result is not None:
            try:
                import baostock as bs
                with contextlib.redirect_stdout(io.StringIO()):
                    bs.logout()
            except Exception:
                pass

    has_current = bool(result["current"])
    has_history = result["history"].get("observations", 0) > 0
    result["status"] = "observed" if has_current and has_history else "partial" if has_current or has_history else "fetch_failed"
    return _json_safe(result)


def _baostock_price_history(symbol, start_date, end_date):
    symbol = str(symbol).strip().zfill(6)
    if _market_prefix(symbol) == "bj":
        raise ValueError("Baostock price history does not support Beijing symbols")
    import baostock as bs

    login_result = None
    try:
        with contextlib.redirect_stdout(io.StringIO()):
            login_result = bs.login()
        if login_result.error_code != "0":
            raise RuntimeError(login_result.error_msg)
        query = bs.query_history_k_data_plus(
            f"{_market_prefix(symbol)}.{symbol}",
            "date,code,open,high,low,close,volume,amount,turn,tradestatus,pctChg",
            start_date=start_date.isoformat(),
            end_date=end_date.isoformat(),
            frequency="d",
            adjustflag="3",
        )
        if query.error_code != "0":
            raise RuntimeError(query.error_msg)
        rows = []
        while query.next():
            rows.append(query.get_row_data())
        frame = pd.DataFrame(rows, columns=query.fields)
        if frame.empty:
            raise ValueError("Baostock returned no price-history rows")
        if "tradestatus" in frame.columns:
            frame = frame[frame["tradestatus"] == "1"]
        frame = frame.rename(columns={
            "date": "日期",
            "open": "开盘",
            "high": "最高",
            "low": "最低",
            "close": "收盘",
            "volume": "成交量",
            "amount": "成交额",
            "turn": "换手率",
            "pctChg": "涨跌幅",
        })
        return frame
    finally:
        if login_result is not None:
            with contextlib.redirect_stdout(io.StringIO()):
                bs.logout()


def _technical_summary(frame):
    required = {"日期", "收盘", "最高", "最低", "成交量"}
    if frame is None or frame.empty or not required.issubset(frame.columns):
        missing = sorted(required.difference(set(frame.columns if frame is not None else [])))
        raise ValueError(f"historical frame missing columns: {missing}")

    data = frame.copy()
    data["日期"] = pd.to_datetime(data["日期"], errors="coerce")
    numeric_columns = ["开盘", "收盘", "最高", "最低", "成交量", "成交额", "换手率"]
    for column in numeric_columns:
        if column in data.columns:
            data[column] = pd.to_numeric(data[column], errors="coerce")
    data = data.dropna(subset=["日期", "收盘"]).sort_values("日期").reset_index(drop=True)
    if len(data) < 2:
        raise ValueError("not enough historical rows for short-term analysis")

    close = data["收盘"]
    high = data["最高"]
    low = data["最低"]
    volume = data["成交量"]
    latest_close = _to_float(close.iloc[-1])

    returns = {}
    for days in [1, 3, 5, 10, 20]:
        if len(close) > days and _to_float(close.iloc[-days - 1]) not in (None, 0):
            returns[f"return_{days}d_pct"] = round((latest_close / float(close.iloc[-days - 1]) - 1) * 100, 2)
        else:
            returns[f"return_{days}d_pct"] = None

    moving_averages = {}
    for days in [5, 10, 20, 60]:
        moving_averages[f"ma{days}"] = round(float(close.tail(days).mean()), 4) if len(close) >= days else None

    ema12 = close.ewm(span=12, adjust=False).mean()
    ema26 = close.ewm(span=26, adjust=False).mean()
    dif = ema12 - ema26
    dea = dif.ewm(span=9, adjust=False).mean()
    macd_hist = (dif - dea) * 2

    delta = close.diff()
    gain = delta.clip(lower=0).ewm(alpha=1 / 14, adjust=False).mean()
    loss = (-delta.clip(upper=0)).ewm(alpha=1 / 14, adjust=False).mean()
    rs = gain / loss.where(loss != 0)
    rsi = 100 - (100 / (1 + rs))
    rsi = rsi.where(loss != 0, 100.0)
    rsi = rsi.where(~((gain == 0) & (loss == 0)), 50.0)

    previous_close = close.shift(1)
    true_range = pd.concat([
        high - low,
        (high - previous_close).abs(),
        (low - previous_close).abs(),
    ], axis=1).max(axis=1)
    atr14 = true_range.rolling(14).mean()
    atr_value = _to_float(atr14.iloc[-1])

    def range_metrics(days):
        window = data.tail(days)
        window_high = _to_float(window["最高"].max())
        window_low = _to_float(window["最低"].min())
        position = None
        if latest_close is not None and window_high is not None and window_low is not None and window_high > window_low:
            position = round((latest_close - window_low) * 100 / (window_high - window_low), 2)
        drawdown = None
        if latest_close is not None and window_high not in (None, 0):
            drawdown = round((latest_close / window_high - 1) * 100, 2)
        return {
            "low": window_low,
            "high": window_high,
            "position_pct": position,
            "drawdown_from_high_pct": drawdown,
        }

    latest_volume = _to_float(volume.iloc[-1])
    average_volume_5 = _to_float(volume.tail(5).mean()) if len(volume) >= 5 else None
    average_volume_20 = _to_float(volume.tail(20).mean()) if len(volume) >= 20 else None
    summary = {
        "as_of": data["日期"].iloc[-1].date().isoformat(),
        "observations": int(len(data)),
        "latest_close": latest_close,
        **returns,
        **moving_averages,
        "macd_dif": round(float(dif.iloc[-1]), 4),
        "macd_dea": round(float(dea.iloc[-1]), 4),
        "macd_hist": round(float(macd_hist.iloc[-1]), 4),
        "rsi14": round(float(rsi.iloc[-1]), 2) if _to_float(rsi.iloc[-1]) is not None else None,
        "atr14": atr_value,
        "atr14_pct": round(atr_value * 100 / latest_close, 2) if atr_value is not None and latest_close not in (None, 0) else None,
        "latest_volume": latest_volume,
        "volume_vs_5d": round(latest_volume / average_volume_5, 2) if latest_volume is not None and average_volume_5 not in (None, 0) else None,
        "volume_vs_20d": round(latest_volume / average_volume_20, 2) if latest_volume is not None and average_volume_20 not in (None, 0) else None,
        "turnover_rate_pct": _to_float(data["换手率"].iloc[-1]) if "换手率" in data.columns else None,
        "range_20d": range_metrics(20),
        "range_60d": range_metrics(60),
    }
    return _json_safe(summary)


def _fund_flow_summary(symbol):
    symbol = str(symbol).strip().zfill(6)
    market = _market_prefix(symbol)
    if market == "bj":
        return {"status": "not_applicable", "reason": "AKShare fund-flow endpoint does not support Beijing symbols"}
    errors = []
    try:
        frame = ak.stock_individual_fund_flow(stock=symbol, market=market)
        if frame is None or frame.empty:
            raise ValueError("fund-flow endpoint returned no rows")
        date_column = "日期" if "日期" in frame.columns else frame.columns[0]
        frame = frame.copy()
        frame[date_column] = pd.to_datetime(frame[date_column], errors="coerce")
        frame = frame.dropna(subset=[date_column]).sort_values(date_column)
        main_column = next((column for column in frame.columns if "主力净流入" in str(column) and "净额" in str(column)), None)
        result = {
            "status": "observed",
            "as_of": frame[date_column].iloc[-1].date().isoformat(),
            "source": "东方财富个股资金流（AKShare）",
            "main_net_inflow": {},
        }
        if main_column:
            values = pd.to_numeric(frame[main_column], errors="coerce")
            for days in [1, 3, 5, 10]:
                result["main_net_inflow"][f"sum_{days}d"] = _to_float(values.tail(days).sum(min_count=1))
            result["main_net_inflow"]["field"] = main_column
        else:
            result["main_net_inflow"]["status"] = "not_found_after_fallback"

        latest = frame.iloc[-1]
        selected = {}
        for column in frame.columns:
            name = str(column)
            if any(keyword in name for keyword in ["收盘价", "涨跌幅", "主力", "超大单", "大单"]):
                selected[name] = latest[column]
        result["latest"] = selected
        return _json_safe(result)
    except Exception as exc:
        errors.append({"source": "akshare.stock_individual_fund_flow", "error": str(exc)})

    periods = {}
    for indicator in ["即时"]:
        try:
            with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                frame = ak.stock_fund_flow_individual(symbol=indicator)
            if frame is None or frame.empty:
                raise ValueError("THS fund-flow ranking returned no rows")
            code_column = next((column for column in frame.columns if "代码" in str(column)), None)
            if code_column is None:
                raise ValueError("THS fund-flow response has no code column")
            matches = frame[frame[code_column].astype(str).str.extract(r"(\d+)", expand=False).str.zfill(6) == symbol]
            if matches.empty:
                raise ValueError(f"symbol {symbol} not found in {indicator}")
            row = matches.iloc[0]
            selected = {}
            for column in frame.columns:
                name = str(column)
                if any(keyword in name for keyword in ["代码", "简称", "最新价", "涨跌幅", "换手率", "流入", "流出", "净额", "净占比"]):
                    selected[name] = row[column]
            periods[indicator] = _json_safe(selected)
        except Exception as exc:
            errors.append({"source": f"akshare.stock_fund_flow_individual({indicator})", "error": str(exc)})

    if periods:
        return {
            "status": "observed",
            "as_of": datetime.now().astimezone().isoformat(timespec="seconds"),
            "source": "同花顺个股资金流排行（AKShare fallback）",
            "periods": periods,
            "limitations": "该备用源仅提供当前资金流；多日资金流由东方财富历史接口或后续定向补数提供",
            "fallback_errors": errors,
        }
    raise RuntimeError(json.dumps(errors, ensure_ascii=False))


def _financial_summary(symbol):
    symbol = str(symbol).strip().zfill(6)
    errors = []
    try:
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            frame = ak.stock_financial_abstract_new_ths(symbol=symbol, indicator="按报告期")
        if frame is None or frame.empty:
            raise ValueError("THS financial endpoint returned no rows")
        date_column = "report_date"
        if date_column not in frame.columns:
            raise ValueError("THS financial response has no report_date")
        frame = frame.copy()
        frame[date_column] = pd.to_datetime(frame[date_column], errors="coerce")
        frame = frame.dropna(subset=[date_column]).sort_values(date_column, ascending=False)
        latest_date = frame[date_column].iloc[0]
        latest = frame[frame[date_column] == latest_date]
        keywords = ["营业总收入", "营业收入", "归属净利润", "扣非净利润", "经营现金流", "资产负债率", "净资产收益率", "毛利率"]
        if "metric_name" in latest.columns:
            latest = latest[latest["metric_name"].astype(str).apply(lambda item: any(keyword in item for keyword in keywords))]
        fields = [column for column in ["report_date", "report_name", "report_period", "quarter_name", "metric_name", "value", "single", "yoy", "mom", "single_yoy"] if column in latest.columns]
        return {
            "status": "observed",
            "as_of": latest_date.date().isoformat(),
            "source": "同花顺财务指标（AKShare）",
            "metrics": _json_safe(latest[fields].head(30).to_dict("records")),
        }
    except Exception as exc:
        errors.append({"source": "akshare.stock_financial_abstract_new_ths", "error": str(exc)})

    try:
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            frame = ak.stock_financial_analysis_indicator_em(symbol=_eastmoney_code(symbol), indicator="按报告期")
        if frame is None or frame.empty:
            raise ValueError("Eastmoney financial endpoint returned no rows")
        frame = frame.copy()
        date_column = "REPORT_DATE" if "REPORT_DATE" in frame.columns else "REPORT_DATE_NAME"
        frame[date_column] = pd.to_datetime(frame[date_column], errors="coerce")
        frame = frame.sort_values(date_column, ascending=False)
        latest = frame.iloc[0]
        selected_fields = [
            "REPORT_DATE", "REPORT_DATE_NAME", "TOTALOPERATEREVE", "PARENTNETPROFIT", "KCFJCXSYJLR",
            "TOTALOPERATEREVETZ", "PARENTNETPROFITTZ", "ROEJQ", "XSMLL", "ZCFZL", "MGJYXJJE",
        ]
        metrics = {field: latest[field] for field in selected_fields if field in frame.columns}
        return {
            "status": "observed",
            "as_of": latest[date_column].date().isoformat() if pd.notna(latest[date_column]) else None,
            "source": "东方财富财务分析（AKShare）",
            "metrics": _json_safe(metrics),
            "fallback_errors": errors,
        }
    except Exception as exc:
        errors.append({"source": "akshare.stock_financial_analysis_indicator_em", "error": str(exc)})
        return {"status": "fetch_failed", "errors": errors}


def get_stock_research(symbol, years=5):
    """Compact evidence bundle for the investment-advisor pipeline."""
    symbol = str(symbol).strip().zfill(6)
    fetched_at = datetime.now().astimezone().isoformat(timespec="seconds")
    result = {
        "symbol": symbol,
        "market": "A股",
        "fetched_at": fetched_at,
        "valuation": get_stock_valuation(symbol, years=years),
        "short_term": {},
        "fund_flow": {},
        "financials": {},
        "sources": [],
        "errors": [],
    }

    end = datetime.now().date()
    start = end - timedelta(days=550)
    history = None
    history_source = None
    try:
        history = ak.stock_zh_a_hist(
            symbol=symbol,
            period="daily",
            start_date=start.strftime("%Y%m%d"),
            end_date=end.strftime("%Y%m%d"),
            adjust="",
        )
        history_source = "东方财富A股历史行情（AKShare）"
    except Exception as exc:
        result["errors"].append({"source": "akshare.stock_zh_a_hist", "error": str(exc)})
        try:
            history = _baostock_price_history(symbol, start, end)
            history_source = "Baostock A股日频行情（fallback）"
        except Exception as fallback_exc:
            result["errors"].append({"source": "baostock.query_history_k_data_plus(price)", "error": str(fallback_exc)})

    if history is not None:
        try:
            result["short_term"] = _technical_summary(history)
            result["short_term"]["status"] = "observed"
            result["short_term"]["source"] = history_source
            result["sources"].append({"name": history_source, "as_of": result["short_term"].get("as_of")})
        except Exception as exc:
            result["short_term"] = {"status": "fetch_failed", "error": str(exc)}
            result["errors"].append({"source": "technical_summary", "error": str(exc)})
    else:
        result["short_term"] = {"status": "fetch_failed", "error": "all price-history sources failed"}

    try:
        result["fund_flow"] = _fund_flow_summary(symbol)
    except Exception as exc:
        result["fund_flow"] = {"status": "fetch_failed", "error": str(exc)}
        result["errors"].append({"source": "akshare.stock_individual_fund_flow", "error": str(exc)})

    result["financials"] = _financial_summary(symbol)
    if result["financials"].get("status") == "fetch_failed":
        result["errors"].extend(result["financials"].get("errors", []))

    valuation_status = result["valuation"].get("status")
    short_term_status = result["short_term"].get("status", "observed" if result["short_term"] else "fetch_failed")
    financial_status = result["financials"].get("status")
    result["coverage"] = {
        "valuation": valuation_status,
        "short_term": short_term_status,
        "fund_flow": result["fund_flow"].get("status", "observed" if result["fund_flow"] else "fetch_failed"),
        "financials": financial_status,
    }
    observed = sum(status == "observed" for status in result["coverage"].values())
    result["status"] = "observed" if observed == len(result["coverage"]) else "partial" if observed else "fetch_failed"
    return _json_safe(result)

def get_hk_stock_hist(symbol, period="daily", start_date=None, end_date=None, adjust=""):
    """获取港股历史数据"""
    try:
        df = ak.stock_hk_hist(
            symbol=symbol,
            period=period,
            start_date=start_date,
            end_date=end_date,
            adjust=adjust
        )
        return {
            "symbol": symbol,
            "market": "hk",
            "period": period,
            "adjust": adjust,
            "data": df.to_dict('records'),
            "count": len(df)
        }
    except Exception as e:
        return {"error": f"获取港股历史数据失败: {str(e)}"}

def get_hk_stock_spot():
    """获取港股实时行情"""
    try:
        df = ak.stock_hk_spot_em()
        return {
            "market": "hk",
            "data": df.to_dict('records'),
            "count": len(df)
        }
    except Exception as e:
        return {"error": f"获取港股实时行情失败: {str(e)}"}

def get_us_stock_hist(symbol, adjust="qfq"):
    """获取美股历史数据"""
    try:
        df = ak.stock_us_daily(symbol=symbol, adjust=adjust)
        return {
            "symbol": symbol,
            "market": "us",
            "adjust": adjust,
            "data": df.to_dict('records'),
            "count": len(df)
        }
    except Exception as e:
        return {"error": f"获取美股历史数据失败: {str(e)}"}

def get_us_stock_spot():
    """获取美股实时行情"""
    try:
        df = ak.stock_us_spot_em()
        return {
            "market": "us",
            "data": df.to_dict('records'),
            "count": len(df)
        }
    except Exception as e:
        return {"error": f"获取美股实时行情失败: {str(e)}"}

def get_market_overview():
    """获取市场概览"""
    try:
        df = ak.stock_zh_a_spot_em()

        # 计算市场统计
        total_stocks = len(df)
        up_stocks = len(df[df['涨跌幅'].astype(float) > 0])
        down_stocks = len(df[df['涨跌幅'].astype(float) < 0])
        flat_stocks = len(df[df['涨跌幅'].astype(float) == 0])

        # 获取涨跌停股票
        limit_up = len(df[df['涨跌幅'].astype(float) >= 9.5])
        limit_down = len(df[df['涨跌幅'].astype(float) <= -9.5])

        # 获取涨跌榜
        df_sorted = df.sort_values('涨跌幅', ascending=False)
        top_gainers = df_sorted.head(10)[['代码', '名称', '最新价', '涨跌幅']].to_dict('records')
        top_losers = df_sorted.tail(10)[['代码', '名称', '最新价', '涨跌幅']].to_dict('records')

        return {
            "market_stats": {
                "total_stocks": total_stocks,
                "up_stocks": up_stocks,
                "down_stocks": down_stocks,
                "flat_stocks": flat_stocks,
                "limit_up": limit_up,
                "limit_down": limit_down
            },
            "top_gainers": top_gainers,
            "top_losers": top_losers,
            "update_time": pd.Timestamp.now().strftime("%Y-%m-%d %H:%M:%S")
        }
    except Exception as e:
        return {"error": f"获取市场概览失败: {str(e)}"}

def main():
    parser = argparse.ArgumentParser(description='AKShare 股票数据获取')
    parser.add_argument('--type', required=True,
                       choices=['hist', 'hist_min', 'spot', 'info', 'fundamental', 'valuation', 'research',
                               'hk_hist', 'hk_spot', 'us_hist', 'us_spot', 'overview'],
                       help='数据类型')
    parser.add_argument('--symbol', help='股票代码')
    parser.add_argument('--years', type=int, default=5, help='估值历史窗口年数，默认5年')
    parser.add_argument('--period', default='daily',
                       choices=['daily', 'weekly', 'monthly'],
                       help='周期')
    parser.add_argument('--start', help='开始日期 YYYYMMDD')
    parser.add_argument('--end', help='结束日期 YYYYMMDD')
    parser.add_argument('--adjust', default='',
                       choices=['', 'qfq', 'hfq'],
                       help='复权类型')

    args = parser.parse_args()

    symbol_types = {'hist', 'hist_min', 'info', 'fundamental', 'valuation', 'research', 'hk_hist', 'us_hist'}
    if args.type in symbol_types and not args.symbol:
        parser.error(f"--symbol is required for --type {args.type}")

    if args.type == 'hist':
        result = get_stock_hist(args.symbol, args.period, args.start, args.end, args.adjust)
    elif args.type == 'hist_min':
        period = args.period if args.period in ['1', '5', '15', '30', '60'] else '5'
        result = get_stock_hist_min(args.symbol, period, args.start, args.end, args.adjust)
    elif args.type == 'spot':
        result = get_stock_spot()
    elif args.type == 'info':
        result = get_stock_info(args.symbol)
    elif args.type == 'fundamental':
        result = get_stock_fundamental(args.symbol)
    elif args.type == 'valuation':
        result = get_stock_valuation(args.symbol, years=args.years)
    elif args.type == 'research':
        result = get_stock_research(args.symbol, years=args.years)
    elif args.type == 'hk_hist':
        result = get_hk_stock_hist(args.symbol, args.period, args.start, args.end, args.adjust)
    elif args.type == 'hk_spot':
        result = get_hk_stock_spot()
    elif args.type == 'us_hist':
        result = get_us_stock_hist(args.symbol, args.adjust)
    elif args.type == 'us_spot':
        result = get_us_stock_spot()
    elif args.type == 'overview':
        result = get_market_overview()
    else:
        result = {"error": f"未知类型: {args.type}"}

    print(json.dumps(_json_safe(result), ensure_ascii=False, indent=2, default=str, allow_nan=False))

if __name__ == "__main__":
    main()
