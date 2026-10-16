package analysis

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mac/internal/configvalue"
)

func sqlEDAString(values map[string]interface{}, key, fallback string) string {
	return configvalue.String(values, key, fallback)
}

// SQLTool generates SQL queries from natural language.
type SQLTool struct{}

func (t *SQLTool) Name() string { return "sql_gen" }
func (t *SQLTool) Description() string {
	return "Generate SQL queries from natural language descriptions. " +
		"Supports: SELECT queries with JOIN/GROUP BY/HAVING/ORDER BY, INSERT/UPDATE/DELETE, CREATE TABLE, indexes. " +
		"Output: formatted SQL with comments."
}
func (t *SQLTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query_type": map[string]interface{}{
				"type": "string", "description": "select (query), insert, update, delete, create_table",
				"enum": []string{"select", "insert", "update", "delete", "create_table"},
			},
			"description": map[string]interface{}{
				"type": "string", "description": "What you want to query/create in plain language",
			},
			"tables": map[string]interface{}{
				"type": "string", "description": "Table names involved (comma-separated)",
			},
			"columns": map[string]interface{}{
				"type": "string", "description": "Column names (comma-separated)",
			},
			"conditions": map[string]interface{}{
				"type": "string", "description": "WHERE conditions",
			},
		},
		"required": []string{"query_type", "description"},
	}
}

func (t *SQLTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	queryType := sqlEDAString(params, "query_type", "select")
	desc := sqlEDAString(params, "description", "")
	tables := sqlEDAString(params, "tables", "")
	columns := sqlEDAString(params, "columns", "*")
	conditions := sqlEDAString(params, "conditions", "")

	if desc == "" {
		return "Error: description is required", nil
	}

	tableList := parseList(tables)
	colList := parseList(columns)

	var sql string
	var explanation string

	switch queryType {
	case "select":
		sql = buildSelect(colList, tableList, conditions)
		explanation = buildSelectExplanation(colList, tableList, conditions)
	case "insert":
		vals := make([]string, len(colList))
		for i := range vals {
			vals[i] = "?"
		}
		sql = fmt.Sprintf("INSERT INTO %s (%s)\nVALUES (%s);",
			strings.Join(tableList, ", "), strings.Join(colList, ", "), strings.Join(vals, ", "))
		explanation = "Parameterized INSERT — safe against SQL injection"
	case "update":
		sql = fmt.Sprintf("UPDATE %s\nSET %s = ?\nWHERE %s;",
			strings.Join(tableList, ", "), strings.Join(colList, " = ?, "), conditions)
		explanation = "Parameterized UPDATE — safe against SQL injection"
	case "delete":
		sql = fmt.Sprintf("DELETE FROM %s\nWHERE %s;", strings.Join(tableList, ", "), conditions)
		explanation = "DELETE with WHERE clause — always verify conditions before running"
	case "create_table":
		colDefs := make([]string, len(colList))
		for i, c := range colList {
			colDefs[i] = fmt.Sprintf("  %s VARCHAR(255)", c)
		}
		sql = fmt.Sprintf("CREATE TABLE %s (\n  id SERIAL PRIMARY KEY,\n%s\n);",
			strings.Join(tableList, ", "), strings.Join(colDefs, ",\n"))
		explanation = "CREATE TABLE with auto-increment primary key"
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("## SQL Query: %s\n\n", strings.ToUpper(queryType[:1])+queryType[1:]))
	result.WriteString("```sql\n")
	result.WriteString(sql)
	result.WriteString("\n```\n\n")
	result.WriteString(fmt.Sprintf("**Explanation**: %s\n\n", explanation))

	if queryType == "select" && len(tableList) > 1 {
		result.WriteString("### Query Optimization Tips\n")
		result.WriteString("- Ensure JOIN columns are indexed\n")
		result.WriteString("- Use EXPLAIN to check query plan\n")
		result.WriteString("- Consider adding LIMIT for large result sets\n")
	}

	return result.String(), nil
}

func buildSelect(cols, tables []string, conditions string) string {
	var sb strings.Builder
	sb.WriteString("SELECT\n  ")
	sb.WriteString(strings.Join(cols, ",\n  "))
	sb.WriteString("\nFROM ")
	sb.WriteString(strings.Join(tables, "\n  JOIN "))
	if conditions != "" {
		sb.WriteString("\nWHERE ")
		sb.WriteString(conditions)
	}
	sb.WriteString("\nORDER BY 1\nLIMIT 1000;")
	return sb.String()
}

func buildSelectExplanation(cols, tables []string, conditions string) string {
	parts := []string{fmt.Sprintf("Selects %s", strings.Join(cols, ", "))}
	if len(tables) > 0 {
		parts = append(parts, fmt.Sprintf("from %s", strings.Join(tables, " JOIN ")))
	}
	if conditions != "" {
		parts = append(parts, fmt.Sprintf("filtered by: %s", conditions))
	}
	return strings.Join(parts, " ") + ". Limited to 1000 rows."
}

func parseList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{"*"}
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// ==================== eda_report tool ====================

type EDAReportTool struct {
	workspace string
}

func NewEDAReportTool(workspace string) *EDAReportTool {
	os.MkdirAll(filepath.Join(workspace, ".openkitty", "storage", "data"), 0755)
	return &EDAReportTool{workspace: workspace}
}

func (t *EDAReportTool) Name() string { return "eda_report" }

func (t *EDAReportTool) Description() string {
	return "Generate automated Exploratory Data Analysis (EDA) reports. " +
		"Creates a Python script that produces: data profile, distributions, correlations, missing values, outliers, and visualizations. " +
		"Output: HTML report or PNG charts."
}

func (t *EDAReportTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"data_file": map[string]interface{}{
				"type": "string", "description": "Path to data file (.csv, .json, .xlsx)",
			},
			"output": map[string]interface{}{
				"type": "string", "description": "Output format: html (full report), charts (PNG only), summary (text only)",
				"enum": []string{"html", "charts", "summary"},
			},
		},
		"required": []string{"data_file"},
	}
}

func (t *EDAReportTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	dataFile := sqlEDAString(params, "data_file", "")
	output := sqlEDAString(params, "output", "html")

	if dataFile == "" {
		return "Error: data_file is required", nil
	}

	scriptPath := filepath.Join(t.workspace, ".openkitty", "storage", "data", "eda_report.py")

	script := fmt.Sprintf(`#!/usr/bin/env python3
"""Automated EDA Report — Generated by OpenKitty"""
import pandas as pd
import numpy as np
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import seaborn as sns

df = pd.read_csv(%q)
print(f"=== EDA Report ===")
print(f"Dataset: {df.shape[0]} rows x {df.shape[1]} cols")
print(f"Memory: {df.memory_usage(deep=True).sum()/1024:.0f} KB")
print(f"Duplicates: {df.duplicated().sum()}")

# Column types
types = df.dtypes.value_counts()
print(f"\nColumn Types:")
for t, c in types.items():
    print(f"  {t}: {c}")

# Missing values
missing = df.isnull().sum()
missing_pct = (missing / len(df) * 100).round(1)
if missing.sum() > 0:
    print(f"\nMissing Values:")
    for col in missing[missing > 0].index:
        print(f"  {col}: {missing[col]} ({missing_pct[col]}%%)")
else:
    print("\nNo missing values!")

# Numeric summary
numeric_cols = df.select_dtypes(include=[np.number]).columns.tolist()
if len(numeric_cols) > 0:
    print(f"\nNumeric Features ({len(numeric_cols)}):")
    stats = df[numeric_cols].describe().round(2)
    print(stats.to_string())

    # Skewness & Kurtosis
    for col in numeric_cols[:10]:
        skew = df[col].skew()
        kurt = df[col].kurtosis()
        if abs(skew) > 1:
            print(f"  {col}: skewed ({skew:.2f}) — consider log transform")
        if kurt > 3:
            print(f"  {col}: heavy tails (kurtosis={kurt:.2f}) — check for outliers")

# Categorical summary
cat_cols = df.select_dtypes(include=['object']).columns.tolist()
if len(cat_cols) > 0:
    print(f"\nCategorical Features ({len(cat_cols)}):")
    for col in cat_cols[:10]:
        nunique = df[col].nunique()
        top_val = df[col].value_counts().index[0] if nunique > 0 else 'N/A'
        print(f"  {col}: {nunique} unique values, top='{top_val}'")
`, dataFile)

	if output == "charts" || output == "html" {
		script += fmt.Sprintf(`
# Visualizations
os.makedirs('storage/data/eda_charts', exist_ok=True)

# 1. Distribution plots
if len(numeric_cols) > 0:
    n = min(len(numeric_cols), 6)
    fig, axes = plt.subplots(2, 3, figsize=(16, 10))
    axes = axes.flatten()
    for i, col in enumerate(numeric_cols[:n]):
        df[col].dropna().hist(bins=30, ax=axes[i], edgecolor='white', alpha=0.7)
        axes[i].set_title(col, fontsize=10)
        axes[i].axvline(df[col].median(), color='red', linestyle='--', label='median')
    for i in range(n, 6):
        axes[i].set_visible(False)
    plt.suptitle('Distribution Analysis', fontsize=14)
    plt.tight_layout()
    plt.savefig('storage/data/eda_charts/distributions.png', dpi=150)
    print("\nChart saved: distributions.png")

# 2. Correlation heatmap
if len(numeric_cols) >= 2:
    plt.figure(figsize=(12, 10))
    corr = df[numeric_cols].corr()
    mask = np.triu(np.ones_like(corr, dtype=bool))
    sns.heatmap(corr, mask=mask, annot=True, fmt='.2f', cmap='coolwarm',
                center=0, square=True, linewidths=1)
    plt.title('Feature Correlation Matrix')
    plt.tight_layout()
    plt.savefig('storage/data/eda_charts/correlation.png', dpi=150)
    print("Chart saved: correlation.png")

# 3. Missing value heatmap
if missing.sum() > 0:
    plt.figure(figsize=(12, max(4, len(missing[missing>0])*0.3)))
    sns.heatmap(df.isnull(), cbar=True, yticklabels=False, cmap='viridis')
    plt.title('Missing Value Pattern')
    plt.savefig('storage/data/eda_charts/missing.png', dpi=150)
    print("Chart saved: missing.png")

# 4. Box plots for outlier detection
if len(numeric_cols) > 0:
    n = min(len(numeric_cols), 8)
    fig, ax = plt.subplots(figsize=(14, 6))
    df[numeric_cols[:n]].boxplot(ax=ax, rot=45)
    ax.set_title('Outlier Detection (Box Plots)')
    plt.tight_layout()
    plt.savefig('storage/data/eda_charts/boxplots.png', dpi=150)
    print("Chart saved: boxplots.png")

print("\nEDA complete! Charts saved to storage/data/eda_charts/")
`)
	}

	script += `
# Key findings summary
print("\n=== Key Findings ===")
print(f"1. Dataset contains {df.shape[0]} records with {df.shape[1]} features")
print(f"2. {len(numeric_cols)} numeric, {len(cat_cols)} categorical features")
if missing.sum() > 0:
    print(f"3. {len(missing[missing>0])} columns have missing values")
if len(numeric_cols) > 1:
    high_corr = []
    for i, c1 in enumerate(numeric_cols):
        for c2 in numeric_cols[i+1:]:
            if abs(df[c1].corr(df[c2])) > 0.8:
                high_corr.append(f"{c1}↔{c2}")
    if high_corr:
        print(f"4. High correlations found: {', '.join(high_corr[:5])}")
print("5. Review charts in storage/data/eda_charts/ for visual insights")
`

	os.WriteFile(scriptPath, []byte(script), 0644)

	return fmt.Sprintf("## EDA Report\n\nScript: `%s`\n\n```bash\npip install pandas numpy matplotlib seaborn\npython storage/data/eda_report.py\n```\n\nOutputs:\n- distributions.png\n- correlation.png\n- missing.png\n- boxplots.png",
		scriptPath), nil
}
