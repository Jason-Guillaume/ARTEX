"use client";

import * as React from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import type { Agent, Company, LLMProfile, Project, ProjectPreferences, TaskTemplate } from "@/lib/types";

const emptyPreferences: ProjectPreferences = {
  default_llm_profile_id: null,
  default_agent_key: "",
  default_task_template_id: null,
  default_company_ids: [],
};

export function ProjectDefaults({ project }: { project: Project }) {
  const [preferences, setPreferences] = React.useState<ProjectPreferences>(emptyPreferences);
  const [profiles, setProfiles] = React.useState<LLMProfile[]>([]);
  const [agents, setAgents] = React.useState<Agent[]>([]);
  const [templates, setTemplates] = React.useState<TaskTemplate[]>([]);
  const [companies, setCompanies] = React.useState<Company[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    let alive = true;
    Promise.all([api.projectPreferences(project.id), api.llmProfiles(), api.agents(), api.taskTemplates(), api.companies()])
      .then(([prefs, llmProfiles, agentList, taskTemplates, companyList]) => {
        if (!alive) return;
        setPreferences({ ...emptyPreferences, ...prefs });
        setProfiles(llmProfiles);
        setAgents(agentList.filter((agent) => agent.enabled));
        setTemplates(taskTemplates);
        setCompanies(companyList);
      })
      .catch((error: unknown) => toast.error(error instanceof Error ? error.message : "读取项目默认配置失败"))
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [project.id]);

  async function save() {
    setBusy(true);
    try {
      const updated = await api.saveProjectPreferences(project.id, preferences);
      setPreferences({ ...emptyPreferences, ...updated });
      toast.success("项目默认配置已保存");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存失败");
    } finally {
      setBusy(false);
    }
  }

  const selectClass = "h-9 w-full rounded-md border bg-background px-3 text-sm";
  const editable = project.status === "active";
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">新建任务默认项</CardTitle>
        <CardDescription>只影响当前项目新建的任务和对话；请求中明确填写的内容优先。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4 md:grid-cols-2">
        <div className="grid gap-2">
          <Label>默认 LLM</Label>
          <select
            className={selectClass}
            value={preferences.default_llm_profile_id ?? ""}
            disabled={loading || busy || !editable}
            onChange={(event) => setPreferences((value) => ({ ...value, default_llm_profile_id: event.target.value ? Number(event.target.value) : null }))}
          >
            <option value="">跟随全局激活配置</option>
            {profiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}
          </select>
        </div>
        <div className="grid gap-2">
          <Label>默认 Agent</Label>
          <select
            className={selectClass}
            value={preferences.default_agent_key}
            disabled={loading || busy || !editable}
            onChange={(event) => setPreferences((value) => ({ ...value, default_agent_key: event.target.value }))}
          >
            <option value="">不指定</option>
            {agents.map((agent) => <option key={agent.key} value={agent.key}>{agent.name}</option>)}
          </select>
        </div>
        <div className="grid gap-2">
          <Label>默认任务模板</Label>
          <select
            className={selectClass}
            value={preferences.default_task_template_id ?? ""}
            disabled={loading || busy || !editable}
            onChange={(event) => setPreferences((value) => ({ ...value, default_task_template_id: event.target.value ? Number(event.target.value) : null }))}
          >
            <option value="">不指定</option>
            {templates.map((template) => <option key={template.id} value={template.id}>{template.name}</option>)}
          </select>
        </div>
        <div className="grid gap-2">
          <Label>默认测试企业</Label>
          <select
            className={`${selectClass} h-24`}
            multiple
            value={preferences.default_company_ids.map(String)}
            disabled={loading || busy || !editable}
            onChange={(event) => setPreferences((value) => ({
              ...value,
              default_company_ids: Array.from(event.target.selectedOptions, (option) => Number(option.value)),
            }))}
          >
            {companies.map((company) => <option key={company.id} value={company.id}>{company.name}</option>)}
          </select>
          <span className="text-xs text-muted-foreground">创建任务时自动关联所选企业及其范围规则。</span>
        </div>
        <div className="md:col-span-2">
          {!editable && <span className="mr-3 text-xs text-muted-foreground">归档项目只读</span>}
          <Button onClick={() => void save()} disabled={loading || busy || !editable}>{busy ? "保存中…" : "保存默认配置"}</Button>
        </div>
      </CardContent>
    </Card>
  );
}
