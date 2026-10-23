package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/report"
)

// getProjectReport 生成当前项目的 Markdown 汇总并记录报告导出审计。
func (s *Server) getProjectReport(w http.ResponseWriter, r *http.Request) {
	projectID := db.ProjectFromContext(r.Context())
	if projectID <= 0 {
		projectID = db.DefaultProjectID()
	}
	project, err := s.m.pg.Project(projectID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "项目不存在")
		return
	}
	counts, err := s.m.Assets().CountsByType()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	findings, err := s.m.pg.ListFindingsForExport(db.FindingFilter{}, nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, finding := range findings {
		traffic, trafficErr := s.m.pg.GetFindingTraffic(r.Context(), finding.ID)
		if trafficErr != nil {
			writeErr(w, http.StatusInternalServerError, trafficErr.Error())
			return
		}
		if traffic != nil {
			finding.TrafficBindings = traffic.Bindings
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# 项目报告 — %s\n\n", project.Name)
	fmt.Fprintf(&b, "- **项目说明**：%s\n- **生成时间**：%s\n- **项目状态**：%s\n\n", reportText(project.Description, "未填写"), time.Now().Format("2006-01-02 15:04:05"), project.Status)
	b.WriteString("## 测试范围\n\n")
	companies, err := s.m.Assets().Companies().ListCompanies()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(companies) == 0 {
		b.WriteString("_未配置企业范围。_\n\n")
	} else {
		for _, company := range companies {
			fmt.Fprintf(&b, "### %s\n\n", company.Name)
			if len(company.Scope) == 0 {
				b.WriteString("_未配置范围规则。_\n\n")
				continue
			}
			for _, scope := range company.Scope {
				fmt.Fprintf(&b, "- %s：`%s`", scope.Kind, scope.Raw)
				if scope.Reason != "" {
					fmt.Fprintf(&b, "（%s）", scope.Reason)
				}
				b.WriteByte('\n')
			}
			b.WriteByte('\n')
		}
	}

	var taskCount int
	if err := s.m.pg.QueryRow(`SELECT count(*) FROM tasks WHERE deleted_at IS NULL`).Scan(&taskCount); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	fmt.Fprintf(&b, "## 项目汇总\n\n- 任务：%d\n- 发现：%d\n", taskCount, len(findings))
	types := make([]string, 0, len(counts))
	for assetType := range counts {
		types = append(types, assetType)
	}
	sort.Strings(types)
	for _, assetType := range types {
		fmt.Fprintf(&b, "- %s：%d\n", assetType, counts[assetType])
	}
	b.WriteString("\n## 项目复测\n\n")
	hasRetests := false
	for _, finding := range findings {
		rows, listErr := s.m.pg.ListFindingRetests(finding.ID)
		if listErr != nil {
			writeErr(w, http.StatusInternalServerError, listErr.Error())
			return
		}
		for _, retest := range rows {
			hasRetests = true
			fmt.Fprintf(&b, "- 漏洞 #%d · %s · **%s / %s**：%s", finding.ID, reportText(finding.Name, finding.VulnClass), retest.Status, reportText(retest.Verdict, "未判定"), reportText(retest.Summary, retest.Evidence))
			if retest.Error != "" {
				fmt.Fprintf(&b, "（错误：%s）", retest.Error)
			}
			b.WriteByte('\n')
		}
	}
	if !hasRetests {
		b.WriteString("_暂无复测记录。_\n")
	}
	b.WriteString("\n## 发现与证据\n\n")
	b.WriteString(report.FindingsMarkdown(findings, time.Now()))
	s.auditProject(projectID, project.Name, "report_exported", map[string]any{"format": "markdown", "findings": len(findings)})
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''project-report.md")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

// reportText 为报告中的空文本提供易读的回退内容。
func reportText(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

