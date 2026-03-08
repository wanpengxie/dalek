(function () {
  const pages = {
    overview: { title: "Overview", subtitle: "Overview page" },
    tickets: { title: "Tickets", subtitle: "Tickets page" },
    "merges-inbox": {
      title: "Merges & Inbox",
      subtitle: "Live merge queue and inbox items",
    },
    "planner-runtime": {
      title: "Planner & Runtime",
      subtitle: "Planner execution state for the selected project",
    },
  };

  const COUNT_LABELS = {
    backlog: "Backlog",
    queued: "Queued",
    active: "Active",
    blocked: "Blocked",
    done: "Done",
    archived: "Archived",
  };

  const DEFAULT_COUNT_ORDER = [
    "backlog",
    "queued",
    "active",
    "blocked",
    "done",
    "archived",
  ];

  const asyncRoutes = new Set(["overview", "planner-runtime", "merges-inbox"]);
  let renderNonce = 0;

  function normalizeRoute(hash) {
    const raw = (hash || "").replace(/^#\/?/, "").trim().toLowerCase();
    if (raw && pages[raw]) {
      return raw;
    }
    return "overview";
  }

  function getProjectName() {
    const project = new URLSearchParams(window.location.search).get("project");
    if (!project) {
      return "";
    }
    return project.trim();
  }

  function escapeHtml(value) {
    return String(value)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#39;");
  }

  function formatMaybe(value) {
    if (value === null || value === undefined || value === "") {
      return "N/A";
    }
    return String(value);
  }

  function formatTimestamp(value) {
    if (!value) {
      return "N/A";
    }
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return String(value);
    }
    return date.toLocaleString();
  }

  function renderNav(route) {
    const navLinks = document.querySelectorAll("nav a[data-route]");
    navLinks.forEach((node) => {
      const isActive = node.getAttribute("data-route") === route;
      node.classList.toggle("active", isActive);
      node.setAttribute("aria-current", isActive ? "page" : "false");
    });
  }

  function renderPageHeader(page) {
    return `
      <div class="page-header">
        <h1 class="page-title">${escapeHtml(page.title)}</h1>
        <p class="page-subtitle">${escapeHtml(page.subtitle)}</p>
      </div>
    `;
  }

  function renderStaticPage(page) {
    return `
      ${renderPageHeader(page)}
      <div class="empty-state">
        <p>Static placeholder page.</p>
      </div>
    `;
  }

  function renderProjectRequired(page, route) {
    return `
      ${renderPageHeader(page)}
      <div class="error-panel">
        <p><strong>Missing project query parameter.</strong></p>
        <p>Open this page with <code>?project=&lt;name&gt;</code> in the URL.</p>
        <p class="mono-path">${escapeHtml(
          window.location.pathname + "?project=my-project#/" + route,
        )}</p>
      </div>
    `;
  }

  function renderLoading(page) {
    return `
      ${renderPageHeader(page)}
      <div class="loading-panel">
        <div class="spinner" aria-hidden="true"></div>
        <p>Loading data...</p>
      </div>
    `;
  }

  function renderError(page, err) {
    const message = err instanceof Error ? err.message : String(err);
    return `
      ${renderPageHeader(page)}
      <div class="error-panel">
        <p><strong>Failed to load data.</strong></p>
        <p>${escapeHtml(message)}</p>
      </div>
    `;
  }

  async function fetchJson(path) {
    const response = await fetch(path, {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) {
      let detail = `${response.status} ${response.statusText}`;
      const text = await response.text();
      if (text) {
        detail += `: ${text.slice(0, 180)}`;
      }
      throw new Error(detail);
    }
    return response.json();
  }

  async function fetchOverview(project) {
    const url = new URL("/api/v1/overview", window.location.origin);
    url.searchParams.set("project", project);

    const response = await fetch(url.toString(), {
      method: "GET",
      headers: {
        Accept: "application/json",
      },
    });

    let payload = null;
    try {
      payload = await response.json();
    } catch (_err) {
      payload = null;
    }

    if (!response.ok) {
      const message = payload && payload.cause ? payload.cause : response.statusText;
      throw new Error(message || "request failed");
    }

    return payload || {};
  }

  function badge(text, className) {
    return `<span class="badge ${className}">${escapeHtml(text)}</span>`;
  }

  function mergeStatusClass(status) {
    const classes = {
      proposed: "badge-status-proposed",
      checks_running: "badge-status-checks-running",
      ready: "badge-status-ready",
      approved: "badge-status-approved",
      merged: "badge-status-merged",
      discarded: "badge-status-discarded",
      blocked: "badge-status-blocked",
    };
    return classes[status] || "badge-neutral";
  }

  function inboxStatusClass(status) {
    const classes = {
      open: "badge-status-open",
      done: "badge-status-done",
      snoozed: "badge-status-snoozed",
    };
    return classes[status] || "badge-neutral";
  }

  function inboxSeverityClass(severity) {
    const classes = {
      info: "badge-severity-info",
      warn: "badge-severity-warn",
      blocker: "badge-severity-blocker",
    };
    return classes[severity] || "badge-neutral";
  }

  function createStatSection(title, description, stats) {
    const values = stats && stats.length ? stats : [{ label: "No data", value: "-" }];
    const cards = values
      .map((item) => {
        return `
          <div class="stat-item">
            <span class="stat-value">${escapeHtml(String(valueOrDash(item.value)))}</span>
            <span class="stat-label">${escapeHtml(item.label)}</span>
          </div>
        `;
      })
      .join("");

    return `
      <section class="dashboard-section">
        <h2>${escapeHtml(title)}</h2>
        <p class="section-subtitle">${escapeHtml(description)}</p>
        <div class="stats-grid">${cards}</div>
      </section>
    `;
  }

  function mapToPairs(rawMap, order, aliases) {
    const src = rawMap && typeof rawMap === "object" ? rawMap : {};
    const used = {};
    const pairs = [];

    order.forEach((key) => {
      if (!Object.prototype.hasOwnProperty.call(src, key)) {
        return;
      }
      used[key] = true;
      pairs.push({
        label: aliases[key] || prettifyKey(key),
        value: toNumber(src[key]),
      });
    });

    Object.keys(src)
      .sort()
      .forEach((key) => {
        if (used[key]) {
          return;
        }
        pairs.push({
          label: aliases[key] || prettifyKey(key),
          value: toNumber(src[key]),
        });
      });

    return pairs;
  }

  function prettifyKey(key) {
    if (!key) {
      return "Unknown";
    }
    return String(key)
      .split("_")
      .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
      .join(" ");
  }

  function toNumber(value) {
    if (typeof value === "number" && Number.isFinite(value)) {
      return value;
    }
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : 0;
  }

  function booleanText(value) {
    return value ? "Yes" : "No";
  }

  function valueOrDash(value) {
    if (value === null || value === undefined || value === "") {
      return "-";
    }
    return value;
  }

  async function renderOverviewPage(page, project) {
    const overview = await fetchOverview(project);

    return `
      ${renderPageHeader(page)}
      <div class="dashboard-layout">
        ${createStatSection(
          "Ticket Counts",
          "Current ticket distribution by workflow status",
          mapToPairs(overview.ticket_counts, DEFAULT_COUNT_ORDER, COUNT_LABELS),
        )}
        ${createStatSection(
          "Worker Utilization",
          "Running workers, capacity and blocked workers",
          [
            { label: "Running", value: toNumber(overview.worker_stats && overview.worker_stats.running) },
            {
              label: "Max Running",
              value: toNumber(overview.worker_stats && overview.worker_stats.max_running),
            },
            { label: "Blocked", value: toNumber(overview.worker_stats && overview.worker_stats.blocked) },
          ],
        )}
        ${createStatSection(
          "Planner State",
          "Planner dirty state, wake version and latest runtime metadata",
          [
            { label: "Dirty", value: booleanText(overview.planner_state && overview.planner_state.dirty) },
            {
              label: "Wake Version",
              value: toNumber(overview.planner_state && overview.planner_state.wake_version),
            },
            {
              label: "Active Task Run",
              value: valueOrDash(overview.planner_state && overview.planner_state.active_task_run_id),
            },
            {
              label: "Cooldown Until",
              value: formatTimestamp(overview.planner_state && overview.planner_state.cooldown_until),
            },
            {
              label: "Last Run At",
              value: formatTimestamp(overview.planner_state && overview.planner_state.last_run_at),
            },
            {
              label: "Last Error",
              value: valueOrDash(overview.planner_state && overview.planner_state.last_error),
            },
          ],
        )}
        ${createStatSection(
          "Merge Queue",
          "Merge items grouped by status",
          mapToPairs(overview.merge_counts, [], {}),
        )}
        ${createStatSection(
          "Inbox",
          "Open, snoozed and blocker inbox totals",
          [
            { label: "Open", value: toNumber(overview.inbox_counts && overview.inbox_counts.open) },
            { label: "Snoozed", value: toNumber(overview.inbox_counts && overview.inbox_counts.snoozed) },
            { label: "Blockers", value: toNumber(overview.inbox_counts && overview.inbox_counts.blockers) },
          ],
        )}
      </div>
      <p class="dashboard-footnote">Project: ${escapeHtml(project)}</p>
    `;
  }

  async function renderPlannerRuntimePage(page, project) {
    const planner = await fetchJson(`/api/v1/planner?project=${encodeURIComponent(project)}`);
    const hasActiveRun =
      planner.active_task_run_id !== null &&
      planner.active_task_run_id !== undefined;
    const autoAdvancing = Boolean(planner.dirty || hasActiveRun);
    const runtimeLabel = hasActiveRun ? "running now" : planner.dirty ? "queued work" : "idle";

    return `
      ${renderPageHeader(page)}
      <div class="planner-runtime-state">
        <div>
          <p class="meta-label">Project</p>
          <p class="meta-value mono-path">${escapeHtml(project)}</p>
        </div>
        <div>
          <p class="meta-label">Automatic Progress</p>
          <p class="meta-value">${badge(
            autoAdvancing ? `On (${runtimeLabel})` : "Idle",
            autoAdvancing ? "badge-good" : "badge-neutral",
          )}</p>
        </div>
      </div>
      <div class="info-grid">
        <div class="info-item">
          <span class="info-key">dirty</span>
          <span class="info-value">${badge(
            planner.dirty ? "true" : "false",
            planner.dirty ? "badge-warn" : "badge-good",
          )}</span>
        </div>
        <div class="info-item">
          <span class="info-key">wake_version</span>
          <span class="info-value">${escapeHtml(formatMaybe(planner.wake_version))}</span>
        </div>
        <div class="info-item">
          <span class="info-key">active_task_run_id</span>
          <span class="info-value">${escapeHtml(
            formatMaybe(planner.active_task_run_id),
          )}</span>
        </div>
        <div class="info-item">
          <span class="info-key">cooldown_until</span>
          <span class="info-value">${escapeHtml(formatTimestamp(planner.cooldown_until))}</span>
        </div>
        <div class="info-item">
          <span class="info-key">last_run_at</span>
          <span class="info-value">${escapeHtml(formatTimestamp(planner.last_run_at))}</span>
        </div>
        <div class="info-item">
          <span class="info-key">last_error</span>
          <span class="info-value">${escapeHtml(formatMaybe(planner.last_error))}</span>
        </div>
      </div>
    `;
  }

  function renderMergeRows(merges) {
    if (!merges.length) {
      return `
        <tr>
          <td class="table-empty" colspan="6">No merge items.</td>
        </tr>
      `;
    }

    return merges
      .map((item) => {
        return `
          <tr>
            <td>${escapeHtml(formatMaybe(item.ID))}</td>
            <td>${badge(
              formatMaybe(item.Status),
              mergeStatusClass(String(item.Status || "")),
            )}</td>
            <td>${escapeHtml(formatMaybe(item.TicketID))}</td>
            <td>${escapeHtml(formatMaybe(item.Branch))}</td>
            <td>${escapeHtml(formatMaybe(item.ApprovedBy))}</td>
            <td>${escapeHtml(formatTimestamp(item.CreatedAt))}</td>
          </tr>
        `;
      })
      .join("");
  }

  function renderInboxRows(inbox) {
    if (!inbox.length) {
      return `
        <tr>
          <td class="table-empty" colspan="6">No inbox items.</td>
        </tr>
      `;
    }

    return inbox
      .map((item) => {
        return `
          <tr>
            <td>${escapeHtml(formatMaybe(item.ID))}</td>
            <td>${badge(
              formatMaybe(item.Status),
              inboxStatusClass(String(item.Status || "")),
            )}</td>
            <td>${badge(
              formatMaybe(item.Severity),
              inboxSeverityClass(String(item.Severity || "")),
            )}</td>
            <td>${escapeHtml(formatMaybe(item.Reason))}</td>
            <td>${escapeHtml(formatMaybe(item.TicketID))}</td>
            <td>${escapeHtml(formatMaybe(item.Title))}</td>
          </tr>
        `;
      })
      .join("");
  }

  async function renderMergesInboxPage(page, project) {
    const [merges, inbox] = await Promise.all([
      fetchJson(`/api/v1/merges?project=${encodeURIComponent(project)}`),
      fetchJson(`/api/v1/inbox?project=${encodeURIComponent(project)}`),
    ]);

    const mergeItems = Array.isArray(merges) ? merges : [];
    const inboxItems = Array.isArray(inbox) ? inbox : [];

    return `
      ${renderPageHeader(page)}
      <div class="planner-runtime-state">
        <div>
          <p class="meta-label">Project</p>
          <p class="meta-value mono-path">${escapeHtml(project)}</p>
        </div>
        <div>
          <p class="meta-label">Rows Loaded</p>
          <p class="meta-value">${escapeHtml(
            `${mergeItems.length} merges / ${inboxItems.length} inbox`,
          )}</p>
        </div>
      </div>
      <section class="table-section">
        <h2>Merges</h2>
        <div class="table-wrap">
          <table class="data-table">
            <thead>
              <tr>
                <th>ID</th>
                <th>Status</th>
                <th>Ticket ID</th>
                <th>Branch</th>
                <th>Approved By</th>
                <th>Created At</th>
              </tr>
            </thead>
            <tbody>
              ${renderMergeRows(mergeItems)}
            </tbody>
          </table>
        </div>
      </section>
      <section class="table-section">
        <h2>Inbox</h2>
        <div class="table-wrap">
          <table class="data-table">
            <thead>
              <tr>
                <th>ID</th>
                <th>Status</th>
                <th>Severity</th>
                <th>Reason</th>
                <th>Ticket ID</th>
                <th>Summary</th>
              </tr>
            </thead>
            <tbody>
              ${renderInboxRows(inboxItems)}
            </tbody>
          </table>
        </div>
      </section>
    `;
  }

  async function render(route) {
    const page = pages[route];
    const container = document.getElementById("page-content");
    if (!page || !container) {
      return;
    }

    renderNav(route);
    if (!asyncRoutes.has(route)) {
      container.innerHTML = renderStaticPage(page);
      return;
    }

    const project = getProjectName();
    if (!project) {
      container.innerHTML = renderProjectRequired(page, route);
      return;
    }

    const currentNonce = ++renderNonce;
    container.innerHTML = renderLoading(page);

    try {
      let markup;
      if (route === "overview") {
        markup = await renderOverviewPage(page, project);
      } else if (route === "planner-runtime") {
        markup = await renderPlannerRuntimePage(page, project);
      } else {
        markup = await renderMergesInboxPage(page, project);
      }

      if (currentNonce !== renderNonce) {
        return;
      }
      container.innerHTML = markup;
    } catch (error) {
      if (currentNonce !== renderNonce) {
        return;
      }
      container.innerHTML = renderError(page, error);
    }
  }

  function onRouteChange() {
    const route = normalizeRoute(window.location.hash);
    if (window.location.hash !== "#/" + route) {
      window.location.hash = "#/" + route;
      return;
    }
    render(route);
  }

  window.addEventListener("hashchange", onRouteChange);
  onRouteChange();
})();
