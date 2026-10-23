import assert from "node:assert/strict";
import test from "node:test";
import { clearCurrentProjectID, currentProjectID, setCurrentProjectID } from "./project-session.ts";

function storage() {
  const data = new Map();
  return { getItem: (key) => data.get(key) ?? null, setItem: (key, value) => data.set(key, String(value)), removeItem: (key) => data.delete(key) };
}

test("project selection remains local to each tab while migrating legacy preferences", () => {
  globalThis.window = {};
  globalThis.localStorage = storage();
  const firstTab = storage();
  const secondTab = storage();
  globalThis.sessionStorage = firstTab;
  localStorage.setItem("artex_project_id", "1");
  assert.equal(currentProjectID(), "1");
  setCurrentProjectID(2);
  globalThis.sessionStorage = secondTab;
  assert.equal(currentProjectID(), "1");
  setCurrentProjectID(3);
  globalThis.sessionStorage = firstTab;
  assert.equal(currentProjectID(), "2");
  clearCurrentProjectID();
  assert.equal(currentProjectID(), "");
  globalThis.sessionStorage = secondTab;
  assert.equal(currentProjectID(), "3");
  delete globalThis.window;
  delete globalThis.localStorage;
  delete globalThis.sessionStorage;
});
