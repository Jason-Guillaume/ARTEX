"use client";

import { createContext, type ReactNode, useCallback, useContext, useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { currentProjectID, setCurrentProjectID } from "@/lib/project-session";
import type { Project } from "@/lib/types";

type ProjectContextValue = {
  projects: Project[];
  current: Project;
  refresh: () => Promise<void>;
  choose: (id: number) => void;
};

const ProjectContext = createContext<ProjectContextValue | null>(null);

export function ProjectProvider({ children }: { children: ReactNode }) {
  const [projects, setProjects] = useState<Project[]>([]);
  const [current, setCurrent] = useState<Project | null>(null);
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    const result = await api.projects();
    const items = result.projects ?? [];
    const id = Number(currentProjectID());
    const active = items.find((p) => p.id === id) ?? items.find((p) => p.id === result.current) ?? items[0];
    if (!active) throw new Error("暂无可用项目，请联系管理员检查数据");
    setCurrentProjectID(active.id);
    setProjects(items);
    setCurrent(active);
    setError("");
  }, []);

  useEffect(() => {
    const url = new URL(window.location.href);
    const requested = url.searchParams.get("project");
    if (requested && /^[1-9]\d*$/.test(requested)) {
      setCurrentProjectID(requested);
      url.searchParams.delete("project");
      window.history.replaceState(window.history.state, "", url);
    }
    void refresh().catch((err: unknown) => setError(err instanceof Error ? err.message : "项目加载失败"));
  }, [refresh]);

  function choose(id: number) {
    if (id === current?.id) return;
    window.location.assign(`/dashboard/?project=${id}`);
  }

  if (error && !current) {
    return (
      <div className="flex min-h-svh flex-col items-center justify-center gap-4 p-6" role="alert">
        <p className="text-destructive text-sm">{error}</p>
        <Button variant="outline" onClick={() => void refresh().catch((err: Error) => setError(err.message))}>
          重新加载项目
        </Button>
      </div>
    );
  }

  if (!current) {
    return (
      <div className="flex min-h-svh items-center justify-center text-muted-foreground text-sm">正在加载项目…</div>
    );
  }

  return <ProjectContext.Provider value={{ projects, current, refresh, choose }}>{children}</ProjectContext.Provider>;
}

export function useProject() {
  const value = useContext(ProjectContext);
  if (!value) throw new Error("项目上下文尚未初始化");
  return value;
}
