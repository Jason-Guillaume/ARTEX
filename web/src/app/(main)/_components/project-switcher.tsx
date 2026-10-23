"use client";

import { useState } from "react";

import Link from "next/link";

import { CheckIcon, FolderKanbanIcon, PlusIcon, Settings2Icon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";

import { ProjectEditor } from "./project-editor";
import { useProject } from "./project-provider";

// ProjectSwitcher 展示当前项目状态并提供可搜索的项目切换入口。
export function ProjectSwitcher() {
  const { projects, current, choose } = useProject();
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const matches = projects.filter((p) => `${p.name} ${p.description}`.toLowerCase().includes(search.toLowerCase()));
  return (
    <>
      <DropdownMenu
        onOpenChange={(value) => {
          if (value) setSearch("");
        }}
      >
        <DropdownMenuTrigger asChild>
          <Button
            variant="outline"
            size="sm"
            className="h-8 max-w-[240px] gap-1.5 px-2"
            aria-label={`当前项目：${current.name}`}
          >
            <FolderKanbanIcon className="size-3.5 shrink-0" />
            <span className="truncate">{current.name}</span>
            {current.status === "archived" && <Badge variant="secondary">已归档</Badge>}
            {current.status === "deleting" && <Badge variant="secondary">删除中</Badge>}
            {current.status === "deletion_failed" && <Badge variant="destructive">删除失败</Badge>}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-80">
          <DropdownMenuLabel>切换项目</DropdownMenuLabel>
          <div className="px-2 pb-2">
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="搜索项目名称或说明"
              aria-label="搜索项目"
              onKeyDown={(e) => e.stopPropagation()}
            />
          </div>
          <div className="max-h-72 overflow-y-auto">
            {matches.map((project) => (
              <DropdownMenuItem
                key={project.id}
                className="gap-2"
                disabled={project.status === "deleting"}
                onSelect={() => choose(project.id)}
              >
                <CheckIcon className={project.id === current.id ? "size-4 shrink-0" : "size-4 shrink-0 opacity-0"} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate">{project.name}</span>
                  {project.description && (
                    <span className="block truncate text-muted-foreground text-xs">{project.description}</span>
                  )}
                </span>
                {project.status === "archived" && <span className="text-muted-foreground text-xs">已归档</span>}
                {project.status === "deleting" && <span className="text-muted-foreground text-xs">删除中</span>}
                {project.status === "deletion_failed" && <span className="text-destructive text-xs">删除失败</span>}
              </DropdownMenuItem>
            ))}
            {matches.length === 0 && <p className="px-3 py-4 text-muted-foreground text-sm">没有匹配的项目</p>}
          </div>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={() => setOpen(true)}>
            <PlusIcon />
            新建项目
          </DropdownMenuItem>
          <DropdownMenuItem asChild>
            <Link href="/function/projects/">
              <Settings2Icon />
              管理项目
            </Link>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <ProjectEditor open={open} onOpenChange={setOpen} />
    </>
  );
}
