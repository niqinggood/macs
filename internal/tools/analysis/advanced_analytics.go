package analysis

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mac/internal/configvalue"
)

func analyticsString(values map[string]interface{}, key, fallback string) string {
	return configvalue.String(values, key, fallback)
}

func analyticsInt(values map[string]interface{}, key string, fallback int) int {
	return configvalue.Int(values, key, fallback)
}

// AdvancedAnalyticsTool provides time series, anomaly detection, and NLP capabilities.
type AdvancedAnalyticsTool struct {
	workspace string
}

func NewAdvancedAnalyticsTool(workspace string) *AdvancedAnalyticsTool {
	os.MkdirAll(filepath.Join(workspace, ".openkitty", "storage", "data"), 0755)
	return &AdvancedAnalyticsTool{workspace: workspace}
}

func (t *AdvancedAnalyticsTool) Name() string { return "advanced_analytics" }

func (t *AdvancedAnalyticsTool) Description() string {
	return "Advanced analytics: time series forecasting, anomaly detection, clustering, NLP text analysis. " +
		"Generates executable Python scripts with detailed output."
}

func (t *AdvancedAnalyticsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"analysis": map[string]interface{}{
				"type": "string", "description": "time_series (forecast), anomaly (detection), clustering (segments), nlp (text analysis)",
				"enum": []string{"time_series", "anomaly", "clustering", "nlp"},
			},
			"data_file": map[string]interface{}{
				"type": "string", "description": "Path to data file",
			},
			"date_column": map[string]interface{}{
				"type": "string", "description": "Date/time column name (for time_series)",
			},
			"value_column": map[string]interface{}{
				"type": "string", "description": "Value column to analyze",
			},
			"text_column": map[string]interface{}{
				"type": "string", "description": "Text column (for NLP analysis)",
			},
			"forecast_periods": map[string]interface{}{
				"type": "integer", "description": "Number of periods to forecast (default 30)",
			},
			"clusters": map[string]interface{}{
				"type": "integer", "description": "Number of clusters (default 3)",
			},
		},
		"required": []string{"analysis", "data_file"},
	}
}

func (t *AdvancedAnalyticsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	analysis := analyticsString(params, "analysis", "time_series")
	dataFile := analyticsString(params, "data_file", "")
	dateCol := analyticsString(params, "date_column", "date")
	valueCol := analyticsString(params, "value_column", "value")
	textCol := analyticsString(params, "text_column", "text")
	periods := analyticsInt(params, "forecast_periods", 30)
	clusters := analyticsInt(params, "clusters", 3)

	if dataFile == "" {
		return "Error: data_file is required", nil
	}

	switch analysis {
	case "time_series":
		return t.generateTimeSeriesScript(dataFile, dateCol, valueCol, periods)
	case "anomaly":
		return t.generateAnomalyScript(dataFile, valueCol)
	case "clustering":
		return t.generateClusteringScript(dataFile, clusters)
	case "nlp":
		return t.generateNLPScript(dataFile, textCol)
	}
	return fmt.Sprintf("Unknown analysis: %s", analysis), nil
}

func (t *AdvancedAnalyticsTool) generateTimeSeriesScript(dataFile, dateCol, valueCol string, periods int) (string, error) {
	scriptPath := filepath.Join(t.workspace, ".openkitty", "storage", "data", "time_series_forecast.py")

	script := fmt.Sprintf(`#!/usr/bin/env python3
"""Time Series Forecasting"""
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
from datetime import datetime

df = pd.read_csv(%q)
df[%q] = pd.to_datetime(df[%q])
df = df.sort_values(%q).set_index(%q)
series = df[%q].dropna()

print(f"=== Time Series Analysis ===")
print(f"Periods: {len(series)}")
print(f"Date range: {series.index.min()} to {series.index.max()}")
print(f"NaN count: {series.isna().sum()}")

# Decomposition
from statsmodels.tsa.seasonal import seasonal_decompose
decomp = seasonal_decompose(series.dropna(), model='additive', period=min(7, len(series)//2))
fig, axes = plt.subplots(4, 1, figsize=(14, 12))
axes[0].plot(series); axes[0].set_title('Original')
axes[1].plot(decomp.trend); axes[1].set_title('Trend')
axes[2].plot(decomp.seasonal); axes[2].set_title('Seasonal')
axes[3].plot(decomp.resid); axes[3].set_title('Residual')
plt.tight_layout(); plt.savefig('storage/data/ts_decompose.png', dpi=150)

# Forecast with Prophet or Holt-Winters
try:
    from prophet import Prophet
    pdf = pd.DataFrame({'ds': series.index, 'y': series.values})
    model = Prophet()
    model.fit(pdf)
    future = model.make_future_dataframe(periods=%d)
    forecast = model.predict(future)
    print(f"\nForecast ({len(forecast)} periods):")
    print(forecast[['ds','yhat','yhat_lower','yhat_upper']].tail(%d).to_string())

    fig2 = model.plot(forecast)
    plt.savefig('storage/data/ts_forecast.png', dpi=150)
    print("Forecast chart saved")
except ImportError:
    print("Install prophet: pip install prophet")
    # Fallback: simple moving average
    ma = series.rolling(window=7).mean()
    last_ma = ma.iloc[-1]
    print(f"\nSimple Forecast (7-day MA):")
    for i in range(1, min(%d+1, 8)):
        print(f"  Period +{i}: {last_ma:.2f}")

print("\nAnalysis complete!")
`, dataFile, dateCol, dateCol, dateCol, dateCol, valueCol, periods, min(periods, 10), periods)

	os.WriteFile(scriptPath, []byte(script), 0644)
	return fmt.Sprintf("## Time Series Analysis\n\nScript: `%s`\n\n```bash\npip install pandas matplotlib statsmodels prophet\npython storage/data/time_series_forecast.py\n```", scriptPath), nil
}

func (t *AdvancedAnalyticsTool) generateAnomalyScript(dataFile, valueCol string) (string, error) {
	scriptPath := filepath.Join(t.workspace, ".openkitty", "storage", "data", "anomaly_detection.py")

	script := fmt.Sprintf(`#!/usr/bin/env python3
"""Anomaly Detection"""
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt

df = pd.read_csv(%q)
data = df[%q].dropna()
print(f"=== Anomaly Detection ===")
print(f"Samples: {len(data)}, Mean: {data.mean():.2f}, Std: {data.std():.2f}")

# Method 1: Z-score
z_scores = np.abs((data - data.mean()) / data.std())
anomalies_z = data[z_scores > 3]
print(f"\nZ-Score method (>3 sigma): {len(anomalies_z)} anomalies")

# Method 2: IQR
q1, q3 = data.quantile(0.25), data.quantile(0.75)
iqr = q3 - q1
lower, upper = q1 - 1.5*iqr, q3 + 1.5*iqr
anomalies_iqr = data[(data < lower) | (data > upper)]
print(f"IQR method: {len(anomalies_iqr)} anomalies")

# Method 3: Isolation Forest
from sklearn.ensemble import IsolationForest
iso = IsolationForest(contamination=0.05, random_state=42)
preds = iso.fit_predict(data.values.reshape(-1, 1))
anomalies_if = data[preds == -1]
print(f"Isolation Forest: {len(anomalies_if)} anomalies")

# Plot
fig, axes = plt.subplots(2, 1, figsize=(14, 8))
axes[0].plot(data.index, data.values, 'b-', alpha=0.7, label='Normal')
axes[0].scatter(anomalies_z.index, anomalies_z.values, c='red', s=50, label='Anomalies (Z-score)')
axes[0].set_title('Anomaly Detection Results')
axes[0].legend()
axes[1].boxplot(data.dropna().values, vert=False)
axes[1].set_title('Box Plot with Outliers')
plt.tight_layout()
plt.savefig('storage/data/anomaly_chart.png', dpi=150)

# List anomalies
if len(anomalies_z) > 0:
    print(f"\nTop anomalies:")
    for idx in anomalies_z.sort_values(ascending=False).head(10).index:
        print(f"  Index {idx}: {data[idx]:.2f} (z-score: {z_scores[idx]:.1f})")

print("\nChart saved to storage/data/anomaly_chart.png")
`, dataFile, valueCol)

	os.WriteFile(scriptPath, []byte(script), 0644)
	return fmt.Sprintf("## Anomaly Detection\n\nScript: `%s`\n\n```bash\npip install pandas matplotlib scikit-learn\npython storage/data/anomaly_detection.py\n```", scriptPath), nil
}

func (t *AdvancedAnalyticsTool) generateClusteringScript(dataFile string, clusters int) (string, error) {
	scriptPath := filepath.Join(t.workspace, ".openkitty", "storage", "data", "clustering.py")

	script := fmt.Sprintf(`#!/usr/bin/env python3
"""Customer/Data Segmentation via Clustering"""
import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
from sklearn.preprocessing import StandardScaler
from sklearn.cluster import KMeans
from sklearn.decomposition import PCA

df = pd.read_csv(%q)
numeric_cols = df.select_dtypes(include=[np.number]).columns.tolist()
X = df[numeric_cols].fillna(df[numeric_cols].median())
scaler = StandardScaler()
X_scaled = scaler.fit_transform(X)

# Elbow method
inertias = []
for k in range(1, min(11, len(X))):
    km = KMeans(n_clusters=k, random_state=42, n_init=10)
    km.fit(X_scaled)
    inertias.append(km.inertia_)

print(f"=== Clustering Analysis ===")
print(f"Features: {numeric_cols}")
print(f"Elbow values: {[f'{x:.0f}' for x in inertias]}")

# Fit with specified clusters
k = min(%d, len(X))
km = KMeans(n_clusters=k, random_state=42, n_init=10)
df['cluster'] = km.fit_predict(X_scaled)

# Cluster stats
for c in range(k):
    cluster_data = df[df['cluster'] == c]
    print(f"\nCluster {c} ({len(cluster_data)} samples - {len(cluster_data)/len(df)*100:.1f}%%)")
    print(cluster_data[numeric_cols].mean().to_string())

# PCA visualization
pca = PCA(n_components=2)
X_pca = pca.fit_transform(X_scaled)
plt.figure(figsize=(10, 8))
scatter = plt.scatter(X_pca[:, 0], X_pca[:, 1], c=df['cluster'], cmap='viridis', alpha=0.6)
plt.colorbar(scatter, label='Cluster')
plt.title(f'Clustering Results (PCA projection) - {k} clusters')
plt.savefig('storage/data/clustering_chart.png', dpi=150)
print("\nChart saved to storage/data/clustering_chart.png")
`, dataFile, clusters)

	os.WriteFile(scriptPath, []byte(script), 0644)
	return fmt.Sprintf("## Clustering\n\nScript: `%s`\nClusters: %d\n\n```bash\npip install pandas matplotlib scikit-learn\npython storage/data/clustering.py\n```", scriptPath, clusters), nil
}

func (t *AdvancedAnalyticsTool) generateNLPScript(dataFile, textCol string) (string, error) {
	scriptPath := filepath.Join(t.workspace, ".openkitty", "storage", "data", "nlp_analysis.py")

	script := fmt.Sprintf(`#!/usr/bin/env python3
"""NLP Text Analysis"""
import pandas as pd
import re
from collections import Counter

df = pd.read_csv(%q)
texts = df[%q].dropna().astype(str)
print(f"=== NLP Analysis ===")
print(f"Documents: {len(texts)}")
print(f"Avg length: {texts.str.len().mean():.0f} chars")

# Word frequency
all_words = []
for text in texts:
    words = re.findall(r'[\w]+', text.lower())
    all_words.extend([w for w in words if len(w) > 2])

word_counts = Counter(all_words)
print(f"\nTop 20 words:")
for word, count in word_counts.most_common(20):
    print(f"  {word}: {count}")

# N-gram analysis
from sklearn.feature_extraction.text import CountVectorizer
try:
    vec = CountVectorizer(ngram_range=(2,2), max_features=20, stop_words='english')
    vec.fit(texts)
    bigrams = vec.get_feature_names_out()
    print(f"\nTop bigrams: {', '.join(bigrams[:10])}")
except:
    pass

# Sentiment
try:
    from textblob import TextBlob
    sentiments = texts.apply(lambda x: TextBlob(str(x)).sentiment.polarity)
    print(f"\nSentiment: mean={sentiments.mean():.2f}, pos={sum(sentiments>0)}, neg={sum(sentiments<0)}")
except ImportError:
    print("Install textblob for sentiment: pip install textblob")

# Word cloud
try:
    from wordcloud import WordCloud
    import matplotlib.pyplot as plt
    wc = WordCloud(width=800, height=400, background_color='white').generate(' '.join(all_words))
    plt.figure(figsize=(12,6))
    plt.imshow(wc)
    plt.axis('off')
    plt.savefig('storage/data/wordcloud.png', dpi=150)
    print("Word cloud saved")
except ImportError:
    print("Install wordcloud: pip install wordcloud matplotlib")

print("\nAnalysis complete!")
`, dataFile, textCol)

	os.WriteFile(scriptPath, []byte(script), 0644)
	return fmt.Sprintf("## NLP Analysis\n\nScript: `%s`\n\n```bash\npip install pandas scikit-learn wordcloud matplotlib textblob\npython storage/data/nlp_analysis.py\n```", scriptPath), nil
}

// ==================== ab_test tool ====================

type ABTestTool struct{}

func (t *ABTestTool) Name() string { return "ab_test" }
func (t *ABTestTool) Description() string {
	return "Design and analyze A/B tests. Given control and treatment group data, " +
		"calculates statistical significance, confidence intervals, effect size, and recommendation."
}
func (t *ABTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"control_data": map[string]interface{}{
				"type": "string", "description": "Control group values (comma-separated numbers)",
			},
			"treatment_data": map[string]interface{}{
				"type": "string", "description": "Treatment group values (comma-separated numbers)",
			},
			"metric_name": map[string]interface{}{
				"type": "string", "description": "Metric name (e.g. conversion_rate, revenue)",
			},
			"alpha": map[string]interface{}{
				"type": "number", "description": "Significance level (default 0.05)",
			},
		},
		"required": []string{"control_data", "treatment_data"},
	}
}

func (t *ABTestTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	controlStr := analyticsString(params, "control_data", "")
	treatmentStr := analyticsString(params, "treatment_data", "")
	metric := analyticsString(params, "metric_name", "metric")

	controlVals := parseNumbers(controlStr)
	treatmentVals := parseNumbers(treatmentStr)

	if len(controlVals) < 2 || len(treatmentVals) < 2 {
		return "Error: need at least 2 values in each group", nil
	}

	// Calculate statistics
	ctrlMean := mean(controlVals)
	treatMean := mean(treatmentVals)
	ctrlStd := stdDev(controlVals)
	treatStd := stdDev(treatmentVals)
	lift := (treatMean - ctrlMean) / ctrlMean * 100

	var report strings.Builder
	report.WriteString("## A/B Test Analysis\n\n")
	report.WriteString(fmt.Sprintf("**Metric**: %s\n\n", metric))
	report.WriteString("| | Control (A) | Treatment (B) |\n")
	report.WriteString("|---|---|---|\n")
	report.WriteString(fmt.Sprintf("| Samples | %d | %d |\n", len(controlVals), len(treatmentVals)))
	report.WriteString(fmt.Sprintf("| Mean | %.4f | %.4f |\n", ctrlMean, treatMean))
	report.WriteString(fmt.Sprintf("| Std Dev | %.4f | %.4f |\n", ctrlStd, treatStd))
	report.WriteString(fmt.Sprintf("| **Lift** | - | **%+.2f%%** |\n\n", lift))

	// Simple t-test approximation
	se := 1.96 * (ctrlStd/float64(len(controlVals)) + treatStd/float64(len(treatmentVals)))
	ciLow := lift - se*100
	ciHigh := lift + se*100
	report.WriteString(fmt.Sprintf("**95%% CI**: [%.2f%%, %.2f%%]\n\n", ciLow, ciHigh))

	if lift > 0 && ciLow > 0 {
		report.WriteString("✅ **Significant improvement** — Treatment is statistically better. Recommend launching.\n")
	} else if lift < 0 && ciHigh < 0 {
		report.WriteString("❌ **Significant decline** — Treatment is statistically worse. Do NOT launch.\n")
	} else {
		report.WriteString("⚠️ **Not significant** — Need more data. Continue the test.\n")
	}

	return report.String(), nil
}

func parseNumbers(s string) []float64 {
	var result []float64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		var f float64
		fmt.Sscanf(part, "%f", &f)
		if f != 0 || part == "0" || part == "0.0" {
			result = append(result, f)
		}
	}
	return result
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func stdDev(vals []float64) float64 {
	if len(vals) < 2 {
		return 0
	}
	m := mean(vals)
	sum := 0.0
	for _, v := range vals {
		sum += (v - m) * (v - m)
	}
	variance := sum / float64(len(vals)-1)
	// Simple sqrt approximation for variance
	return approxSqrt(variance)
}

func approxSqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	guess := x / 2
	for i := 0; i < 10; i++ {
		guess = (guess + x/guess) / 2
	}
	return guess
}
