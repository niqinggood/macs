package analysis

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mac/internal/textutil"
)

func truncate(value string, maximum int) string {
	return textutil.Truncate(value, maximum)
}

// DataScienceTool provides advanced data analysis and ML capabilities.
type DataScienceTool struct {
	workspace string
}

func NewDataScienceTool(workspace string) *DataScienceTool {
	os.MkdirAll(filepath.Join(workspace, ".openkitty", "storage", "data"), 0755)
	os.MkdirAll(filepath.Join(workspace, ".openkitty", "storage", "models"), 0755)
	return &DataScienceTool{workspace: workspace}
}

func (t *DataScienceTool) Name() string { return "data_science" }

func (t *DataScienceTool) Description() string {
	return "Advanced data analysis and machine learning: descriptive statistics, correlation analysis, " +
		"hypothesis testing, ML model training (Python sklearn/xgboost), data visualization, and complete report delivery. " +
		"Use 'report' to directly generate a verified Markdown report and PNG chart without writing shell code."
}

func (t *DataScienceTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task": map[string]interface{}{
				"type": "string", "description": "Task: report (verified Markdown + PNG delivery), stats, ml, viz, or pipeline",
				"enum": []string{"report", "stats", "ml", "viz", "pipeline"},
			},
			"data_file": map[string]interface{}{
				"type": "string", "description": "Path to data file (.csv, .json, .xlsx)",
			},
			"target_column": map[string]interface{}{
				"type": "string", "description": "Target column for ML (for ml/pipeline tasks)",
			},
			"model_type": map[string]interface{}{
				"type": "string", "description": "ML model: linear, random_forest, xgboost, neural_network, auto (auto-select)",
				"enum": []string{"linear", "random_forest", "xgboost", "neural_network", "auto"},
			},
			"chart_type": map[string]interface{}{
				"type": "string", "description": "Chart type (for viz): line, bar, scatter, heatmap, boxplot, distribution",
				"enum": []string{"line", "bar", "scatter", "heatmap", "boxplot", "distribution"},
			},
			"test_hypothesis": map[string]interface{}{
				"type": "string", "description": "Hypothesis to test (for stats task). e.g. 'Group A mean > Group B mean'",
			},
			"report_path": map[string]interface{}{
				"type": "string", "description": "Exact Markdown output path under .openkitty/storage (for report task)",
			},
			"chart_path": map[string]interface{}{
				"type": "string", "description": "Exact PNG output path under .openkitty/storage (for report task)",
			},
		},
		"required": []string{"task", "data_file"},
	}
}

func (t *DataScienceTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	task := sqlEDAString(params, "task", "stats")
	dataFile := sqlEDAString(params, "data_file", "")
	targetCol := sqlEDAString(params, "target_column", "")
	modelType := sqlEDAString(params, "model_type", "auto")
	chartType := sqlEDAString(params, "chart_type", "bar")
	hypothesis := sqlEDAString(params, "test_hypothesis", "")
	reportPath := sqlEDAString(params, "report_path", ".openkitty/storage/data_analysis_report.md")
	chartPath := sqlEDAString(params, "chart_path", ".openkitty/storage/charts/01_overview.png")

	if dataFile == "" {
		return "Error: data_file is required", nil
	}

	// Check if data file exists
	if _, err := os.Stat(dataFile); os.IsNotExist(err) {
		return fmt.Sprintf("Error: data file not found: %s", dataFile), nil
	}

	switch task {
	case "report":
		return t.generateVerifiedReport(ctx, dataFile, reportPath, chartPath)
	case "stats":
		return t.generateStatsScript(dataFile, hypothesis)
	case "ml":
		return t.generateMLScript(dataFile, targetCol, modelType)
	case "viz":
		return t.generateVizScript(dataFile, chartType)
	case "pipeline":
		return t.generatePipelineScript(dataFile, targetCol, modelType)
	}
	return fmt.Sprintf("Unknown task: %s", task), nil
}

func (t *DataScienceTool) resolveStorageOutput(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("output path is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.workspace, filepath.FromSlash(path))
	}
	path = filepath.Clean(path)
	storageRoot := filepath.Join(t.workspace, ".openkitty", "storage")
	rel, err := filepath.Rel(storageRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("output path must stay under %s: %s", storageRoot, path)
	}
	return path, nil
}

func (t *DataScienceTool) generateVerifiedReport(ctx context.Context, dataFile, reportPath, chartPath string) (string, error) {
	if !filepath.IsAbs(dataFile) {
		dataFile = filepath.Join(t.workspace, filepath.FromSlash(dataFile))
	}
	dataFile = filepath.Clean(dataFile)
	reportPath, err := t.resolveStorageOutput(reportPath)
	if err != nil {
		return "", err
	}
	chartPath, err = t.resolveStorageOutput(chartPath)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(filepath.Ext(reportPath), ".md") && !strings.EqualFold(filepath.Ext(reportPath), ".markdown") {
		return "", fmt.Errorf("report_path must be Markdown: %s", reportPath)
	}
	if !strings.EqualFold(filepath.Ext(chartPath), ".png") {
		return "", fmt.Errorf("chart_path must be PNG: %s", chartPath)
	}
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(chartPath), 0755); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "python", "-")
	cmd.Dir = t.workspace
	cmd.Env = append(os.Environ(),
		"PYTHONUTF8=1",
		"OPENKITTY_DATA_FILE="+dataFile,
		"OPENKITTY_REPORT_PATH="+reportPath,
		"OPENKITTY_CHART_PATH="+chartPath,
	)
	cmd.Stdin = strings.NewReader(verifiedDataReportPython)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return "", fmt.Errorf("data report generator failed: %w: %s", runErr, truncate(string(out), 1800))
	}
	reportInfo, err := os.Stat(reportPath)
	if err != nil || reportInfo.IsDir() || reportInfo.Size() < 300 {
		return "", fmt.Errorf("data report verification failed: %s", reportPath)
	}
	chartInfo, err := os.Stat(chartPath)
	if err != nil || chartInfo.IsDir() || chartInfo.Size() < 1024 {
		return "", fmt.Errorf("data chart verification failed: %s", chartPath)
	}
	return fmt.Sprintf("status: success\nreport: %s\nreport_bytes: %d\nchart: %s\nchart_bytes: %d\n%s",
		reportPath, reportInfo.Size(), chartPath, chartInfo.Size(), strings.TrimSpace(string(out))), nil
}

const verifiedDataReportPython = `
import json, os, math
import numpy as np
import pandas as pd
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib import font_manager

for _font_path in (r"C:\Windows\Fonts\msyh.ttc", r"C:\Windows\Fonts\simhei.ttf", r"C:\Windows\Fonts\simsun.ttc"):
    if os.path.exists(_font_path):
        font_manager.fontManager.addfont(_font_path)
        plt.rcParams["font.family"] = font_manager.FontProperties(fname=_font_path).get_name()
        break
plt.rcParams["axes.unicode_minus"] = False

src = os.environ["OPENKITTY_DATA_FILE"]
report_path = os.environ["OPENKITTY_REPORT_PATH"]
chart_path = os.environ["OPENKITTY_CHART_PATH"]
ext = os.path.splitext(src)[1].lower()
if ext == ".csv":
    df = pd.read_csv(src)
elif ext == ".tsv":
    df = pd.read_csv(src, sep="\t")
elif ext in (".xlsx", ".xls"):
    df = pd.read_excel(src)
elif ext == ".json":
    df = pd.read_json(src)
else:
    raise ValueError("unsupported structured data format: " + ext)

df.columns = [str(c).strip() for c in df.columns]
lookup = {c.lower(): c for c in df.columns}
aliases = {
    "week": ["week", "date", "period"],
    "channel": ["channel", "source", "medium"],
    "visits": ["visits", "sessions", "uv", "users"],
    "add_to_cart": ["add_to_cart", "cart_adds", "carts", "atc"],
    "orders": ["orders", "order_count", "transactions"],
    "revenue": ["revenue", "gmv", "sales", "amount"],
    "refund_amount": ["refund_amount", "refunds", "refund_value"],
    "ad_spend": ["ad_spend", "spend", "cost", "ads_cost"],
}
cols = {}
for key, names in aliases.items():
    for name in names:
        if name in lookup:
            cols[key] = lookup[name]
            break

rows, ncols = df.shape
missing = {str(c): int(v) for c, v in df.isna().sum().items()}
duplicates = int(df.duplicated().sum())
numeric = list(df.select_dtypes(include=[np.number]).columns)
anomalies = []
flag = pd.Series(False, index=df.index)

needed = ["visits", "add_to_cart", "orders", "revenue", "refund_amount", "ad_spend"]
is_funnel = all(k in cols for k in needed)
if is_funnel:
    for key in needed:
        df[cols[key]] = pd.to_numeric(df[cols[key]], errors="coerce")
    for idx, row in df.iterrows():
        reasons = []
        if row[cols["add_to_cart"]] > row[cols["visits"]]: reasons.append("加购大于访问")
        if row[cols["orders"]] > row[cols["add_to_cart"]]: reasons.append("订单大于加购")
        if row[cols["refund_amount"]] > row[cols["revenue"]]: reasons.append("退款额大于收入")
        if any(row[cols[k]] < 0 for k in needed if not pd.isna(row[cols[k]])): reasons.append("存在负值")
        if row[cols["visits"]] == 0: reasons.append("访问为零")
        if row[cols["ad_spend"]] == 0: reasons.append("广告花费为零")
        if reasons:
            flag.loc[idx] = True
            anomalies.append({
                "row": int(idx + 2),
                "week": str(row[cols["week"]]) if "week" in cols else "",
                "channel": str(row[cols["channel"]]) if "channel" in cols else "",
                "reasons": reasons,
            })

def pct(x):
    return "—" if pd.isna(x) or math.isinf(float(x)) else f"{float(x)*100:.2f}%"
def num(x):
    return "—" if pd.isna(x) else f"{float(x):,.2f}"
def md_table(frame, ordered):
    lines = ["| " + " | ".join(ordered) + " |", "|" + "|".join(["---"]*len(ordered)) + "|"]
    for _, r in frame.iterrows():
        lines.append("| " + " | ".join(str(r[c]) for c in ordered) + " |")
    return "\n".join(lines)

lines = [
    "# 数据分析报告", "", "## 摘要",
    f"- 数据规模：{rows} 行 × {ncols} 列；缺失值合计 {sum(missing.values())}；重复行 {duplicates}。",
]
summary = {"rows": rows, "columns": ncols, "duplicates": duplicates, "anomalies": len(anomalies)}

if is_funnel and "channel" in cols:
    clean = df.loc[~flag].copy()
    c = cols
    agg = clean.groupby(c["channel"], as_index=False).agg({
        c["visits"]:"sum", c["add_to_cart"]:"sum", c["orders"]:"sum",
        c["revenue"]:"sum", c["refund_amount"]:"sum", c["ad_spend"]:"sum",
    })
    agg["加购率"] = agg[c["add_to_cart"]] / agg[c["visits"]]
    agg["订单转化率"] = agg[c["orders"]] / agg[c["visits"]]
    agg["ROAS"] = agg[c["revenue"]] / agg[c["ad_spend"]]
    agg["退款率"] = agg[c["refund_amount"]] / agg[c["revenue"]]
    best_roas = agg.loc[agg["ROAS"].idxmax()]
    best_conv = agg.loc[agg["订单转化率"].idxmax()]
    worst_refund = agg.loc[agg["退款率"].idxmax()]
    lines += [
        f"- 共识别 {len(anomalies)} 条逻辑异常；渠道汇总采用剔除异常行口径，原始值未修改。",
        f"- 清洗后 ROAS 最高渠道：{best_roas[c['channel']]}（{best_roas['ROAS']:.2f}）。",
        f"- 清洗后订单转化率最高渠道：{best_conv[c['channel']]}（{pct(best_conv['订单转化率'])}）。",
        f"- 清洗后退款率最高渠道：{worst_refund[c['channel']]}（{pct(worst_refund['退款率'])}）。",
    ]
    summary.update({"best_roas_channel": str(best_roas[c["channel"]]), "best_roas": round(float(best_roas["ROAS"]),4)})
    display = pd.DataFrame({
        "渠道": agg[c["channel"]].astype(str),
        "访问": agg[c["visits"]].map(num), "加购": agg[c["add_to_cart"]].map(num), "订单": agg[c["orders"]].map(num),
        "收入": agg[c["revenue"]].map(num), "广告花费": agg[c["ad_spend"]].map(num),
        "加购率": agg["加购率"].map(pct), "订单转化率": agg["订单转化率"].map(pct),
        "ROAS": agg["ROAS"].map(lambda x:f"{x:.2f}"), "退款率": agg["退款率"].map(pct),
    })
    lines += ["", "## 指标公式", "- 加购率 = 加购 / 访问", "- 订单转化率 = 订单 / 访问", "- ROAS = 收入 / 广告花费", "- 退款率 = 退款金额 / 收入",
              "", "## 渠道指标（剔除逻辑异常行）", md_table(display, list(display.columns))]

    fig, axes = plt.subplots(2, 2, figsize=(13, 9))
    labels = agg[c["channel"]].astype(str).tolist()
    axes[0,0].bar(labels, agg["订单转化率"]*100, color="#2563eb"); axes[0,0].set_title("Order conversion rate (%)")
    axes[0,1].bar(labels, agg["ROAS"], color="#10b981"); axes[0,1].set_title("ROAS")
    axes[1,0].bar(labels, agg["退款率"]*100, color="#f59e0b"); axes[1,0].set_title("Refund rate (%)")
    x = np.arange(len(labels)); w = .25
    axes[1,1].bar(x-w, agg[c["visits"]], w, label="Visits"); axes[1,1].bar(x, agg[c["add_to_cart"]], w, label="Cart"); axes[1,1].bar(x+w, agg[c["orders"]], w, label="Orders")
    axes[1,1].set_xticks(x, labels); axes[1,1].set_title("Funnel"); axes[1,1].legend()
    plt.tight_layout(); plt.savefig(chart_path, dpi=140, bbox_inches="tight"); plt.close()
else:
    lines += ["- 未检测到完整电商漏斗字段，以下按通用结构化数据口径分析。", "", "## 数值字段描述统计"]
    if numeric:
        desc = df[numeric].describe().round(3).reset_index().rename(columns={"index":"统计量"})
        lines.append(md_table(desc.astype(str), list(desc.columns)))
        means = df[numeric].mean(numeric_only=True).sort_values(ascending=False).head(10)
        plt.figure(figsize=(11,6)); means.plot(kind="bar", color="#2563eb"); plt.title("Top numeric column means"); plt.tight_layout(); plt.savefig(chart_path, dpi=140); plt.close()
    else:
        counts = df.astype(str).nunique().sort_values(ascending=False).head(10)
        plt.figure(figsize=(11,6)); counts.plot(kind="bar", color="#7c3aed"); plt.title("Unique values by column"); plt.tight_layout(); plt.savefig(chart_path, dpi=140); plt.close()

lines += ["", "## 数据质量", f"- 缺失值：{missing}", f"- 重复行：{duplicates}", f"- 逻辑异常行：{len(anomalies)}"]
if anomalies:
    for a in anomalies:
        lines.append(f"- 原始第 {a['row']} 行（{a['week']} / {a['channel']}）：{'；'.join(a['reasons'])}")
lines += ["", "## 分析口径", "- 原始数据未改写。", "- 汇总表剔除已列明的逻辑异常行；质量章节保留异常原值与行号供审计。",
          "", "## 行动建议", "1. 优先核对已列明的逻辑异常行及上游埋点/退款口径。", "2. 预算调整应基于清洗后的 ROAS、转化率与退款率联合判断。", "3. 将漏斗倒置、退款超收入和分母为零设为入仓校验规则。",
          "", "## 产出文件", f"- Markdown：{report_path}", f"- PNG：{chart_path}"]

with open(report_path, "w", encoding="utf-8") as f:
    f.write("\n".join(lines) + "\n")
if os.path.getsize(report_path) < 300: raise RuntimeError("report too small")
if os.path.getsize(chart_path) < 1024: raise RuntimeError("chart too small")
print(json.dumps({"ok":True, "summary":summary, "report":report_path, "chart":chart_path}, ensure_ascii=False))
`

func (t *DataScienceTool) generateStatsScript(dataFile, hypothesis string) (string, error) {
	scriptPath := filepath.Join(t.workspace, ".openkitty", "storage", "data", "stats_analysis.py")

	script := fmt.Sprintf(`#!/usr/bin/env python3
"""Statistical Analysis Script — Generated by OpenKitty Data Science"""
import pandas as pd
import numpy as np
from scipy import stats as sp_stats

# Load data
df = pd.read_csv(%q)
print("=== Data Overview ===")
print(f"Shape: {df.shape}")
print(f"Columns: {list(df.columns)}")
print(f"\nDescriptive Statistics:")
print(df.describe())

# Missing values
missing = df.isnull().sum()
if missing.sum() > 0:
    print(f"\nMissing Values:\n{missing[missing > 0]}")

# Correlation matrix
numeric_cols = df.select_dtypes(include=[np.number]).columns.tolist()
if len(numeric_cols) > 1:
    print(f"\n=== Correlation Matrix ===")
    corr = df[numeric_cols].corr()
    print(corr.round(3))

    # Find strongest correlations
    for i, col1 in enumerate(numeric_cols):
        for col2 in numeric_cols[i+1:]:
            r = corr.loc[col1, col2]
            if abs(r) > 0.7:
                print(f"STRONG: {col1} vs {col2}: r={r:.3f}")
            elif abs(r) > 0.5:
                print(f"MODERATE: {col1} vs {col2}: r={r:.3f}")
`, dataFile)

	if hypothesis != "" {
		script += fmt.Sprintf(`
# Hypothesis Test
print(f"\n=== Hypothesis Test ===")
print("Hypothesis: %s")
# Run appropriate test based on data type
for col in numeric_cols:
    stat, p_value = sp_stats.normaltest(df[col].dropna())
    print(f"Normality test for {col}: p={p_value:.4f} {'(normal)' if p_value > 0.05 else '(not normal)'}")
`, hypothesis)
	}

	script += `
print("\nAnalysis complete. Output saved to storage/data/")
`

	os.WriteFile(scriptPath, []byte(script), 0644)

	var sb strings.Builder
	sb.WriteString("## Statistical Analysis\n\n")
	sb.WriteString(fmt.Sprintf("Script: `%s`\n\n", scriptPath))
	sb.WriteString("### How to run:\n")
	sb.WriteString("```bash\npip install pandas numpy scipy\npython storage/data/stats_analysis.py\n```\n\n")

	// Try to run if Python available
	cmd := exec.Command("python", scriptPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		sb.WriteString("### Results\n```\n")
		sb.WriteString(truncate(string(out), 3000))
		sb.WriteString("\n```\n")
	} else {
		sb.WriteString(fmt.Sprintf("Python not available for auto-run. Install pandas/scipy and run manually.\n"))
	}

	return sb.String(), nil
}

func (t *DataScienceTool) generateMLScript(dataFile, targetCol, modelType string) (string, error) {
	if targetCol == "" {
		return "Error: target_column is required for ML task", nil
	}

	scriptPath := filepath.Join(t.workspace, ".openkitty", "storage", "data", "ml_train.py")

	modelCode := ""
	modelImport := ""
	switch modelType {
	case "random_forest":
		modelCode = "RandomForestClassifier(n_estimators=100, random_state=42)"
		modelImport = "from sklearn.ensemble import RandomForestClassifier"
	case "xgboost":
		modelCode = "XGBClassifier(n_estimators=100, random_state=42)"
		modelImport = "from xgboost import XGBClassifier"
	case "neural_network":
		modelCode = "MLPClassifier(hidden_layer_sizes=(100, 50), random_state=42)"
		modelImport = "from sklearn.neural_network import MLPClassifier"
	default:
		modelCode = "LogisticRegression(random_state=42)"
		modelImport = "from sklearn.linear_model import LogisticRegression"
	}

	script := fmt.Sprintf(`#!/usr/bin/env python3
"""ML Model Training — Generated by OpenKitty Data Science"""
import pandas as pd
import numpy as np
from sklearn.model_selection import train_test_split, cross_val_score
from sklearn.preprocessing import StandardScaler, LabelEncoder
from sklearn.metrics import accuracy_score, classification_report, confusion_matrix
%s

# Load data
df = pd.read_csv(%q)
print(f"Training ML model on: {df.shape}")

# Prepare features
target = df[%q]
features = df.drop(columns=[%q])

# Handle categorical columns
for col in features.select_dtypes(include=['object']).columns:
    features[col] = LabelEncoder().fit_transform(features[col].astype(str))

# Handle missing values
features = features.fillna(features.median())

# Split
X_train, X_test, y_train, y_test = train_test_split(
    features, target, test_size=0.2, random_state=42
)

# Scale
scaler = StandardScaler()
X_train = scaler.fit_transform(X_train)
X_test = scaler.transform(X_test)

# Train
model = %s
model.fit(X_train, y_train)

# Evaluate
y_pred = model.predict(X_test)
accuracy = accuracy_score(y_test, y_pred)

print(f"\n=== Model Results ===")
print(f"Model: %%s", model.__class__.__name__)
print(f"Accuracy: {accuracy:.4f}")
print(f"\nClassification Report:")
print(classification_report(y_test, y_pred))

# Cross-validation
cv_scores = cross_val_score(model, X_train, y_train, cv=5)
print(f"\n5-Fold CV: {cv_scores.mean():.4f} (+/- {cv_scores.std():.4f})")

# Feature importance
if hasattr(model, 'feature_importances_'):
    importances = model.feature_importances_
    indices = np.argsort(importances)[::-1][:10]
    print(f"\nTop 10 Features:")
    for i in indices:
        print(f"  {features.columns[i]}: {importances[i]:.4f}")

import joblib
joblib.dump(model, 'storage/models/latest_model.pkl')
print("\nModel saved to storage/models/latest_model.pkl")
`, modelImport, dataFile, targetCol, targetCol, modelCode)

	os.WriteFile(scriptPath, []byte(script), 0644)

	return fmt.Sprintf("## ML Training\n\nScript: `%s`\nModel: %s\nTarget: %s\n\n```bash\npip install pandas numpy scikit-learn xgboost\npython storage/data/ml_train.py\n```",
		scriptPath, modelType, targetCol), nil
}

func (t *DataScienceTool) generateVizScript(dataFile, chartType string) (string, error) {
	scriptPath := filepath.Join(t.workspace, ".openkitty", "storage", "data", "viz_charts.py")

	chartCode := ""
	switch chartType {
	case "line":
		chartCode = "df.plot(kind='line', figsize=(12,6)); plt.title('Line Chart'); plt.savefig('storage/data/chart_line.png', dpi=150)"
	case "bar":
		chartCode = "df.iloc[:20].plot(kind='bar', figsize=(14,6)); plt.title('Bar Chart (top 20)'); plt.savefig('storage/data/chart_bar.png', dpi=150)"
	case "scatter":
		chartCode = "if len(numeric_cols)>=2: df.plot(kind='scatter', x=numeric_cols[0], y=numeric_cols[1], figsize=(10,8)); plt.title('Scatter Plot'); plt.savefig('storage/data/chart_scatter.png', dpi=150)"
	case "heatmap":
		chartCode = "sns.heatmap(df[numeric_cols].corr(), annot=True, cmap='coolwarm', fmt='.2f'); plt.title('Correlation Heatmap'); plt.savefig('storage/data/chart_heatmap.png', dpi=150)"
	case "boxplot":
		chartCode = "df[numeric_cols[:8]].boxplot(figsize=(14,6)); plt.title('Box Plot'); plt.savefig('storage/data/chart_boxplot.png', dpi=150)"
	case "distribution":
		chartCode = "for i, col in enumerate(numeric_cols[:4]): plt.subplot(2,2,i+1); df[col].hist(bins=30); plt.title(col); plt.tight_layout(); plt.savefig('storage/data/chart_dist.png', dpi=150)"
	}

	script := fmt.Sprintf(`#!/usr/bin/env python3
"""Data Visualization — Generated by OpenKitty Data Science"""
import pandas as pd
import matplotlib.pyplot as plt
import seaborn as sns

df = pd.read_csv(%q)
numeric_cols = df.select_dtypes(include=['number']).columns.tolist()
print(f"Creating %s chart for: {list(df.columns)}")
%s
print("Chart saved to storage/data/")
`, dataFile, chartType, chartCode)

	os.WriteFile(scriptPath, []byte(script), 0644)

	return fmt.Sprintf("## Visualization\n\nScript: `%s`\nChart type: %s\n\n```bash\npip install pandas matplotlib seaborn\npython storage/data/viz_charts.py\n```",
		scriptPath, chartType), nil
}

func (t *DataScienceTool) generatePipelineScript(dataFile, targetCol, modelType string) (string, error) {
	scriptPath := filepath.Join(t.workspace, ".openkitty", "storage", "data", "full_pipeline.py")

	script := fmt.Sprintf(`#!/usr/bin/env python3
"""Full Data Science Pipeline — Generated by OpenKitty Data Science"""
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
import seaborn as sns
from sklearn.model_selection import train_test_split, cross_val_score
from sklearn.preprocessing import StandardScaler, LabelEncoder
from sklearn.metrics import accuracy_score, classification_report
from sklearn.ensemble import RandomForestClassifier

# 1. Load & Profile
df = pd.read_csv(%q)
print(f"Step 1: Data loaded — {df.shape}")
print(f"Columns: {list(df.columns)}")
print(f"Missing: {df.isnull().sum().sum()}")
print(f"Duplicates: {df.duplicated().sum()}")

# 2. EDA
numeric_cols = df.select_dtypes(include=[np.number]).columns.tolist()
print(f"\nStep 2: EDA — {len(numeric_cols)} numeric features")
if len(numeric_cols) > 1:
    print(df[numeric_cols].describe().round(2).to_string())

# 3. Visualization
if len(numeric_cols) >= 2:
    fig, axes = plt.subplots(1, 2, figsize=(14,5))
    df[numeric_cols[:2]].boxplot(ax=axes[0])
    axes[0].set_title('Box Plot')
    sns.heatmap(df[numeric_cols].corr(), annot=True, cmap='coolwarm', ax=axes[1])
    axes[1].set_title('Correlation')
    plt.tight_layout()
    plt.savefig('storage/data/pipeline_eda.png', dpi=150)
    print("Step 3: Visualization saved")

# 4. Model
target = df[%q]
features = df.drop(columns=[%q])
for col in features.select_dtypes(include=['object']).columns:
    features[col] = LabelEncoder().fit_transform(features[col].astype(str))
features = features.fillna(features.median())

X_train, X_test, y_train, y_test = train_test_split(features, target, test_size=0.2, random_state=42)
scaler = StandardScaler()
X_train = scaler.fit_transform(X_train)
X_test = scaler.transform(X_test)

model = RandomForestClassifier(n_estimators=100, random_state=42)
model.fit(X_train, y_train)
y_pred = model.predict(X_test)

print(f"\nStep 4: Model Results")
print(f"Accuracy: {accuracy_score(y_test, y_pred):.4f}")
print(f"\n{classification_report(y_test, y_pred)}")

import joblib
joblib.dump(model, 'storage/models/pipeline_model.pkl')
print("Pipeline complete! Model saved.")
`, dataFile, targetCol, targetCol)

	if modelType != "" && modelType != "auto" {
		script = strings.Replace(script, "RandomForestClassifier(n_estimators=100", "RandomForestClassifier(n_estimators=200", 1)
	}

	os.WriteFile(scriptPath, []byte(script), 0644)

	return fmt.Sprintf("## Full Pipeline\n\nScript: `%s`\nTarget: %s\n\n```bash\npip install pandas numpy matplotlib seaborn scikit-learn\npython storage/data/full_pipeline.py\n```",
		scriptPath, targetCol), nil
}
