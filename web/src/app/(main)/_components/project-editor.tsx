"use client";

import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
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
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { Project } from "@/lib/types";

import { useProject } from "./project-provider";

export function ProjectEditor({
  open,
  onOpenChange,
  project,
}: {
  open: boolean;
  onOpenChange: (value: boolean) => void;
  project?: Project;
}) {
  const { refresh } = useProject();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const saveLabel = project ? "保存" : "创建并进入";
  useEffect(() => {
    if (open) {
      setName(project?.name ?? "");
      setDescription(project?.description ?? "");
      setError("");
    }
  }, [open, project]);
  async function save(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const input = { name: name.trim(), description: description.trim() };
      if (project) {
        await api.updateProject(project.id, input);
        await refresh();
        onOpenChange(false);
      } else {
        const created = await api.createProject(input);
        window.location.assign(`/dashboard/?project=${created.id}`);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存失败");
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (!busy) onOpenChange(value);
      }}
    >
      <DialogContent>
        <form onSubmit={save} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{project ? "编辑项目" : "新建项目"}</DialogTitle>
            <DialogDescription>
              项目分别保存任务、资产、发现、流量和对话。LLM、Agent、工具和连接配置共用。
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="project-name">项目名称</Label>
            <Input
              id="project-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={80}
              required
              autoFocus
              disabled={busy}
              placeholder="例如：客户 A 安全测试"
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="project-description">项目说明</Label>
            <Textarea
              id="project-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              maxLength={500}
              disabled={busy}
              placeholder="测试范围、背景或备注（可选）"
            />
          </div>
          {error && (
            <p className="text-destructive text-sm" role="alert">
              {error}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
              取消
            </Button>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? "保存中…" : saveLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
