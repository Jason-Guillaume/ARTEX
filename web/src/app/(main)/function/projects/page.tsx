"use client";

import { useEffect, useState } from "react";

import { ArchiveIcon, FolderKanbanIcon, PencilIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { ProjectEditor } from "@/app/(main)/_components/project-editor";
import { useProject } from "@/app/(main)/_components/project-provider";
import { ProjectDefaults } from "./project-defaults";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { clearCurrentProjectID } from "@/lib/project-session";
import type { Project, ProjectAuditEntry } from "@/lib/types";

// ProjectsPage 提供项目配置、生命周期操作、删除重试和操作审计。
export default function ProjectsPage() {
  const { projects, current, choose, refresh } = useProject();
  const [search, setSearch] = useState("");
  const [editor, setEditor] = useState<{ project?: Project } | null>(null);
  const [action, setAction] = useState<{ project: Project; kind: "archive" | "delete" } | null>(null);
  const [confirmation, setConfirmation] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [auditOpen, setAuditOpen] = useState(false);
  const [auditBusy, setAuditBusy] = useState(false);
  const [auditItems, setAuditItems] = useState<ProjectAuditEntry[]>([]);
  const filtered = projects.filter((p) => `${p.name} ${p.description}`.toLowerCase().includes(search.toLowerCase()));
  const deletionPending = projects.some((p) => p.deletion_job?.state === "queued" || p.deletion_job?.state === "running");
  const actionLabel = action?.kind === "delete" ? "永久删除" : "归档";

  useEffect(() => {
    if (!deletionPending) return;
    const timer = window.setInterval(() => void refresh().catch(() => {}), 2000);
    return () => window.clearInterval(timer);
  }, [deletionPending, refresh]);

  // perform 执行归档、恢复或创建持久化删除作业，并刷新项目状态。
  async function perform(project: Project, kind: "archive" | "restore" | "delete") {
    setBusy(true);
    setError("");
    try {
      if (kind === "delete") {
        await api.deleteProject(project.id, confirmation);
        if (project.id === current.id) {
          clearCurrentProjectID();
          window.location.replace("/function/projects/");
          return;
        }
        await refresh();
        setAction(null);
        toast.success("项目数据清理已进入队列；完成后项目会从列表移除");
        return;
      } else if (kind === "archive") await api.archiveProject(project.id);
      else await api.restoreProject(project.id);
      await refresh();
      setAction(null);
      toast.success({ archive: "项目已归档", restore: "项目已恢复", delete: "项目已删除" }[kind]);
    } catch (err) {
      const message = err instanceof Error ? err.message : "操作失败";
      setError(message);
      if (kind === "restore") toast.error(message);
    } finally {
      setBusy(false);
    }
  }
  // toggleAudit 读取或收起最近的跨项目审计记录。
  async function toggleAudit() {
    if (auditOpen) {
      setAuditOpen(false);
      return;
    }
    setAuditBusy(true);
    try {
      setAuditItems(await api.projectAuditHistory(100));
      setAuditOpen(true);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "读取项目审计记录失败");
    } finally {
      setAuditBusy(false);
    }
  }
  function confirm(project: Project, kind: "archive" | "delete") {
    setAction({ project, kind });
    setConfirmation("");
    setError("");
  }
  return (
    <div className="mx-auto flex w-full max-w-6xl flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="font-semibold text-2xl">项目管理</h1>
          <p className="mt-1 text-muted-foreground text-sm">按项目组织任务与数据，归档后保留历史并停止业务写入。</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" disabled={auditBusy} onClick={() => void toggleAudit()}>
            {auditBusy ? "读取中…" : auditOpen ? "收起审计" : "审计记录"}
          </Button>
          <Button
            variant="outline"
            disabled={busy}
            onClick={() => void refresh().catch((err: Error) => toast.error(err.message))}
          >
            <RefreshCwIcon className="size-4" />
            刷新
          </Button>
          <Button onClick={() => setEditor({})}>
            <PlusIcon className="size-4" />
            新建项目
          </Button>
        </div>
      </div>
      {auditOpen && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">项目审计记录</CardTitle>
            <CardDescription>最近 100 条项目创建、修改、归档、删除与报告导出操作。</CardDescription>
          </CardHeader>
          <CardContent className="grid gap-2">
            {auditItems.length === 0 ? (
              <p className="text-muted-foreground text-sm">暂无审计记录</p>
            ) : auditItems.map((item) => (
              <div key={item.id} className="grid gap-1 rounded-md border p-3 text-sm md:grid-cols-[180px_1fr_auto] md:items-center">
                <span className="text-muted-foreground">{new Date(item.created_at).toLocaleString("zh-CN")}</span>
                <span><b>{item.project_name}</b> · {auditActionLabel(item.action)} · {item.actor}</span>
                <details className="text-xs">
                  <summary className="cursor-pointer text-muted-foreground">详情</summary>
                  <pre className="mt-2 max-w-full overflow-auto whitespace-pre-wrap">{JSON.stringify(item.details, null, 2)}</pre>
                </details>
              </div>
            ))}
          </CardContent>
        </Card>
      )}
      <ProjectDefaults project={current} />
      <div className="flex flex-wrap items-center gap-3">
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-sm"
          placeholder="搜索项目名称或说明"
          aria-label="搜索项目"
        />
        <span className="text-muted-foreground text-sm">
          共 {projects.length} 个项目 · {projects.filter((p) => p.status === "active").length} 个正常项目
        </span>
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        {filtered.map((project) => (
          <Card key={project.id} className={project.id === current.id ? "border-primary/60" : ""}>
            <CardHeader>
              <div className="flex items-center justify-between gap-2">
                <CardTitle className="flex min-w-0 items-center gap-2 text-base">
                  <FolderKanbanIcon className="size-4 shrink-0" />
                  <span className="truncate">{project.name}</span>
                </CardTitle>
                <div className="flex shrink-0 gap-1">
                  {project.is_default && <Badge variant="outline">默认</Badge>}
                  {project.id === current.id && <Badge>当前</Badge>}
                  {project.status === "archived" && <Badge variant="secondary">已归档</Badge>}
                  {project.status === "deleting" && <Badge variant="secondary">删除中</Badge>}
                  {project.status === "deletion_failed" && <Badge variant="destructive">删除失败</Badge>}
                </div>
              </div>
              <CardDescription className="min-h-10 whitespace-pre-wrap break-words">
                {project.description || "暂无项目说明"}
              </CardDescription>
            </CardHeader>
            <CardContent className="grid gap-4">
              <div className="grid grid-cols-2 gap-y-3 rounded-md bg-muted/50 p-3 text-center md:grid-cols-4">
                {[
                  { label: "任务", value: project.task_count ?? 0, detail: "" },
                  { label: "资产", value: project.asset_count ?? 0, detail: "" },
                  { label: "发现", value: project.finding_count ?? 0, detail: "" },
                  {
                    label: "Token 用量",
                    value: (project.llm_input_tokens ?? 0) + (project.llm_output_tokens ?? 0),
                    detail: `${(project.llm_calls ?? 0).toLocaleString("zh-CN")} 次调用`,
                  },
                ].map(({ label, value, detail }) => (
                  <div key={label}>
                    <p className="font-semibold text-lg tabular-nums">{value.toLocaleString("zh-CN")}</p>
                    <p className="text-muted-foreground text-xs">{label}</p>
                    {detail && <p className="text-muted-foreground text-[10px]">{detail}</p>}
                  </div>
                ))}
              </div>
              <p className="text-muted-foreground text-xs">
                创建于 {new Date(project.created_at).toLocaleString("zh-CN")}
              </p>
              {project.deletion_job?.state === "failed" && (
                <p className="text-destructive text-xs" role="alert">
                  阶段：{project.deletion_job.phase || "未知"} · {project.deletion_job.error || "清理失败，可重试"}
                </p>
              )}
              {project.status === "deleting" && (
                <p className="text-muted-foreground text-xs">
                  正在清理项目数据（{project.deletion_job?.phase || "等待处理"}），页面会自动刷新。
                </p>
              )}
              <div className="flex flex-wrap gap-2">
                <Button
                  size="sm"
                  variant={project.id === current.id ? "secondary" : "default"}
                  disabled={busy || project.id === current.id || project.status === "deleting"}
                  onClick={() => choose(project.id)}
                >
                  {project.id === current.id ? "当前项目" : project.status === "deleting" ? "删除中" : "进入项目"}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy || project.status !== "active"}
                  onClick={() => setEditor({ project })}
                >
                  <PencilIcon className="size-3.5" />
                  编辑
                </Button>
                {!project.is_default && project.status !== "deleting" && (
                  project.status === "archived" || project.status === "deletion_failed" ? (
                    <>
                      {project.status === "archived" && (
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={busy}
                          onClick={() => void perform(project, "restore")}
                        >
                          恢复
                        </Button>
                      )}
                      <Button size="sm" variant="outline" disabled={busy} onClick={() => confirm(project, "delete")}>
                        <Trash2Icon className="size-3.5" />
                        {project.status === "deletion_failed" ? "重试删除" : "删除"}
                      </Button>
                    </>
                  ) : (
                    <Button size="sm" variant="outline" disabled={busy} onClick={() => confirm(project, "archive")}>
                      <ArchiveIcon className="size-3.5" />
                      归档
                    </Button>
                  ))}
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
      {filtered.length === 0 && <p className="py-12 text-center text-muted-foreground text-sm">没有匹配的项目</p>}
      <ProjectEditor
        open={editor !== null}
        onOpenChange={(open) => {
          if (!open) setEditor(null);
        }}
        project={editor?.project}
      />
      <Dialog
        open={action !== null}
        onOpenChange={(open) => {
          if (!open && !busy) setAction(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{action?.kind === "delete" ? "永久删除项目" : "归档项目"}</DialogTitle>
            <DialogDescription>
              {action?.kind === "delete"
                ? "将永久清理项目的数据库、流量、任务归档包和项目文件。清理过程会持久记录；失败时可在此查看失败阶段并重试。"
                : `归档「${action?.project.name ?? ""}」后，仍可查看历史数据。请先结束运行中的任务、对话及复测，归档后可随时恢复。`}
            </DialogDescription>
          </DialogHeader>
          {action?.kind === "delete" && (
            <div className="grid gap-2">
              <Label htmlFor="confirm-project-name">输入项目名称「{action.project.name}」确认</Label>
              <Input
                id="confirm-project-name"
                value={confirmation}
                onChange={(e) => setConfirmation(e.target.value)}
                autoComplete="off"
              />
            </div>
          )}
          {error && (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          )}
          <DialogFooter>
            <Button variant="outline" disabled={busy} onClick={() => setAction(null)}>
              取消
            </Button>
            <Button
              variant={action?.kind === "delete" ? "destructive" : "default"}
              disabled={busy || (action?.kind === "delete" && confirmation !== action.project.name)}
              onClick={() => {
                if (action) void perform(action.project, action.kind);
              }}
            >
              {busy ? "处理中…" : actionLabel}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// auditActionLabel 将后端审计事件代码转换为界面文案。
function auditActionLabel(action: string) {
  const labels: Record<string, string> = {
    created: "创建",
    updated: "修改",
    archived: "归档",
    restored: "恢复",
    delete_started: "开始删除",
    deleted: "删除完成",
    report_exported: "导出报告",
  };
  return labels[action] ?? action;
}
