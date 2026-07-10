package protocol

import "fmt"

type DailyUsage struct {
	Date            string  `json:"date"`
	CallCount       int     `json:"call_count"`
	AvgDuration     float64 `json:"avg_duration"`
	TotalTokens     int     `json:"total_tokens"`
	TotalPrompt     int     `json:"total_prompt"`
	TotalCompletion int     `json:"total_completion"`
	ModelCount      int     `json:"model_count"`
}

type ModelUsage struct {
	Model           string  `json:"model"`
	CallCount       int     `json:"call_count"`
	AvgDuration     float64 `json:"avg_duration"`
	TotalTokens     int     `json:"total_tokens"`
	TotalPrompt     int     `json:"total_prompt"`
	TotalCompletion int     `json:"total_completion"`
}

type VendorUsage struct {
	Vendor          string  `json:"vendor"`
	CallCount       int     `json:"call_count"`
	AvgDuration     float64 `json:"avg_duration"`
	TotalTokens     int     `json:"total_tokens"`
	TotalPrompt     int     `json:"total_prompt"`
	TotalCompletion int     `json:"total_completion"`
}

func (ut *UsageTracker) GetDailyStats(since, until string) ([]DailyUsage, error) {
	where := ""
	args := []any{}
	if since != "" {
		where += " AND created_at >= ?"
		args = append(args, since)
	}
	if until != "" {
		where += " AND created_at <= ?"
		args = append(args, until)
	}
	if where != "" {
		where = " WHERE " + where[5:]
	}
	q := fmt.Sprintf(`SELECT DATE(created_at) as day, COUNT(*), AVG(duration), COALESCE(SUM(total_tokens),0), COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0), COUNT(DISTINCT model) FROM usage_records%s GROUP BY day ORDER BY day DESC`, where)
	ut.mu.RLock()
	rows, err := ut.db.Query(q, args...)
	if err != nil {
		ut.mu.RUnlock()
		return nil, err
	}
	defer func() {
		rows.Close()
		ut.mu.RUnlock()
	}()
	var stats []DailyUsage
	for rows.Next() {
		var d DailyUsage
		if err := rows.Scan(&d.Date, &d.CallCount, &d.AvgDuration, &d.TotalTokens, &d.TotalPrompt, &d.TotalCompletion, &d.ModelCount); err != nil {
			return nil, err
		}
		stats = append(stats, d)
	}
	return stats, rows.Err()
}

func (ut *UsageTracker) GetModelStats() ([]ModelUsage, error) {
	q := `SELECT model, COUNT(*), AVG(duration), COALESCE(SUM(total_tokens),0), COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0) FROM usage_records GROUP BY model ORDER BY COUNT(*) DESC`
	ut.mu.RLock()
	rows, err := ut.db.Query(q)
	if err != nil {
		ut.mu.RUnlock()
		return nil, err
	}
	defer func() {
		rows.Close()
		ut.mu.RUnlock()
	}()
	var stats []ModelUsage
	for rows.Next() {
		var m ModelUsage
		if err := rows.Scan(&m.Model, &m.CallCount, &m.AvgDuration, &m.TotalTokens, &m.TotalPrompt, &m.TotalCompletion); err != nil {
			return nil, err
		}
		stats = append(stats, m)
	}
	return stats, rows.Err()
}

func (ut *UsageTracker) GetVendorStats() ([]VendorUsage, error) {
	q := `SELECT vendor, COUNT(*), AVG(duration), COALESCE(SUM(total_tokens),0), COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0) FROM usage_records GROUP BY vendor ORDER BY COUNT(*) DESC`
	ut.mu.RLock()
	rows, err := ut.db.Query(q)
	if err != nil {
		ut.mu.RUnlock()
		return nil, err
	}
	defer func() {
		rows.Close()
		ut.mu.RUnlock()
	}()
	var stats []VendorUsage
	for rows.Next() {
		var v VendorUsage
		if err := rows.Scan(&v.Vendor, &v.CallCount, &v.AvgDuration, &v.TotalTokens, &v.TotalPrompt, &v.TotalCompletion); err != nil {
			return nil, err
		}
		stats = append(stats, v)
	}
	return stats, rows.Err()
}
