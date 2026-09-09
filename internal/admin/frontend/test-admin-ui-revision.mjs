import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const appJsPath = resolve(here, "../web/app.js");
const appCssPath = resolve(here, "../web/app.css");

const appJs = readFileSync(appJsPath, "utf8");
const appCss = readFileSync(appCssPath, "utf8");

console.log("Starting NextSQL Admin UI/UX Revision automated verification...");

// Test 1: Verify bundle contains the new icons and components
assert.ok(appJs.includes("User & Workspace Settings"), "Bundle must contain User & Workspace Settings modal title");
assert.ok(appJs.includes("Authenticated Principal"), "Bundle must contain Authenticated Principal section");
assert.ok(appJs.includes("Console Preferences"), "Bundle must contain Console Preferences tab");
assert.ok(appJs.includes("Table Density & Formatting"), "Bundle must contain Table Density setting");
assert.ok(appJs.includes("Default Table Page Size"), "Bundle must contain Page Size setting");
assert.ok(appJs.includes("Monitor Auto-Refresh Interval"), "Bundle must contain Auto-Refresh setting");

// Test 2: Verify @bzync/rui Pagination component is integrated
assert.ok(appJs.includes("aria-label\":\"Pagination\"") || appJs.includes("aria-label:\"Pagination\""), "Bundle must contain Pagination component markup");
assert.ok(appJs.includes("nsm-table-container"), "Bundle must include nsm-table-container");
assert.ok(appJs.includes("nsm-table-search-input"), "Bundle must include nsm-table-search-input");
assert.ok(appJs.includes("nsm-th-sortable"), "Bundle must include sortable table header classes");
assert.ok(appJs.includes("nsm-page-size-selector"), "Bundle must include page size selector");

// Test 3: Verify sidebar icons and group headers
assert.ok(appJs.includes("nsm-nav-group-icon"), "Sidebar must include group icons");
assert.ok(appJs.includes("nsm-sidebar-user-btn"), "Sidebar footer must include interactive user profile button");
assert.ok(appJs.includes("nsm-topbar-profile-btn"), "Topbar must include interactive user profile button");

// Test 4: Verify CSS classes exist in app.css
assert.ok(appCss.includes(".nsm-table-container"), "CSS must define .nsm-table-container");
assert.ok(appCss.includes(".nsm-th-sortable"), "CSS must define .nsm-th-sortable");
assert.ok(appCss.includes(".nsm-table-footer"), "CSS must define .nsm-table-footer");
assert.ok(appCss.includes(".nsm-page-size-btn"), "CSS must define .nsm-page-size-btn");
assert.ok(appCss.includes(".nsm-topbar-profile-btn"), "CSS must define .nsm-topbar-profile-btn");
assert.ok(appCss.includes(".nsm-sidebar-user-btn"), "CSS must define .nsm-sidebar-user-btn");

// Test 6: Verify Studio ResultGrid selection enhancements
assert.ok(appJs.includes("Select all"), "ResultGrid must contain 'Select all' action");
assert.ok(appJs.includes("Unselect all"), "ResultGrid must contain 'Unselect all' action");
assert.ok(appJs.includes("aria-label:\"Select all rows\"") || appJs.includes("aria-label\":\"Select all rows\""), "ResultGrid header must include master checkbox");

// Test 7: Verify Tabs component is integrated across multi-table views
assert.ok(appJs.includes("value:\"sessions\"") || appJs.includes("value\":\"sessions\""), "Activity must use Tabs for sessions");
assert.ok(appJs.includes("value:\"catalog\"") || appJs.includes("value\":\"catalog\""), "Databases must use Tabs for the catalog tree");
assert.ok(appJs.includes("value:\"grants\"") || appJs.includes("value\":\"grants\""), "Security must use Tabs for grants");
assert.ok(appJs.includes("value:\"replication\"") || appJs.includes("value\":\"replication\""), "Overview / Cluster must use Tabs for replication");
assert.ok(appJs.includes("value:\"table_stats\"") || appJs.includes("value\":\"table_stats\""), "Maintenance must use Tabs for table_stats");

console.log("All NextSQL Admin UI/UX Revision tests passed successfully!");

