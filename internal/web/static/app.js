(function () {
  const pages = {
    overview: { title: "Overview", subtitle: "Overview page" },
    tickets: { title: "Tickets", subtitle: "Tickets page" },
    "merges-inbox": {
      title: "Merges & Inbox",
      subtitle: "Merges & Inbox page",
    },
    "planner-runtime": {
      title: "Planner & Runtime",
      subtitle: "Planner & Runtime page",
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

  let renderToken = 0;

  function normalizeRoute(hash) {
    const raw = (hash || "").replace(/^#\/?/, "").trim().toLowerCase();
    if (raw && pages[raw]) {
      return raw;
    }
    return "overview";
  }

  function render(route) {
    renderToken += 1;
    const currentToken = renderToken;
    const page = pages[route];
    const title = document.getElementById("page-title");
    const subtitle = document.getElementById("page-subtitle");
    const content = getPageContentNode();
    if (title) {
      title.textContent = page.title;
    }
    if (subtitle) {
      subtitle.textContent = page.subtitle;
    }

    const navLinks = document.querySelectorAll("nav a[data-route]");
    navLinks.forEach((node) => {
      const isActive = node.getAttribute("data-route") === route;
      node.classList.toggle("active", isActive);
      node.setAttribute("aria-current", isActive ? "page" : "false");
    });

    if (route === "overview") {
      renderOverview(content, currentToken);
      return;
    }
    renderPlaceholder(content, page.subtitle);
  }

  function onRouteChange() {
    const route = normalizeRoute(window.location.hash);
    if (window.location.hash !== "#/" + route) {
      window.location.hash = "#/" + route;
      return;
    }
    render(route);
  }

  function getPageContentNode() {
    const pageCard = document.querySelector("main.content section.page-card");
    if (!pageCard) {
      return null;
    }
    let content = document.getElementById("page-content");
    if (!content) {
      content = document.createElement("div");
      content.id = "page-content";
      pageCard.appendChild(content);
    }
    return content;
  }

  function getProjectName() {
    const search = new URLSearchParams(window.location.search);
    const project = search.get("project");
    if (!project) {
      return "";
    }
    return project.trim();
  }

  function renderPlaceholder(content, text) {
    if (!content) {
      return;
    }
    content.innerHTML = "";
    const empty = document.createElement("p");
    empty.className = "page-state";
    empty.textContent = text;
    content.appendChild(empty);
  }

  function renderOverview(content, token) {
    if (!content) {
      return;
    }
    const project = getProjectName();
    if (!project) {
      content.innerHTML = "";
      const error = document.createElement("p");
      error.className = "page-state state-error";
      error.textContent = "Missing project. Open the page with ?project=<name>.";
      content.appendChild(error);
      return;
    }

    content.innerHTML = "";
    const loading = document.createElement("p");
    loading.className = "page-state state-loading";
    loading.textContent = "Loading overview dashboard...";
    content.appendChild(loading);

    fetchOverview(project)
      .then((overview) => {
        if (token !== renderToken) {
          return;
        }
        renderOverviewDashboard(content, project, overview);
      })
      .catch((err) => {
        if (token !== renderToken) {
          return;
        }
        content.innerHTML = "";
        const error = document.createElement("p");
        error.className = "page-state state-error";
        error.textContent = "Failed to load overview: " + err.message;
        content.appendChild(error);
      });
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

  function renderOverviewDashboard(content, project, overview) {
    content.innerHTML = "";
    const dashboard = document.createElement("div");
    dashboard.className = "dashboard-layout";
    content.appendChild(dashboard);

    dashboard.appendChild(
      createStatSection(
        "Ticket Counts",
        "Current ticket distribution by workflow status",
        mapToPairs(overview.ticket_counts, DEFAULT_COUNT_ORDER, COUNT_LABELS),
      ),
    );
    dashboard.appendChild(
      createStatSection(
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
      ),
    );
    dashboard.appendChild(
      createStatSection(
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
      ),
    );
    dashboard.appendChild(
      createStatSection(
        "Merge Queue",
        "Merge items grouped by status",
        mapToPairs(overview.merge_counts, [], {}),
      ),
    );
    dashboard.appendChild(
      createStatSection(
        "Inbox",
        "Open, snoozed and blocker inbox totals",
        [
          { label: "Open", value: toNumber(overview.inbox_counts && overview.inbox_counts.open) },
          { label: "Snoozed", value: toNumber(overview.inbox_counts && overview.inbox_counts.snoozed) },
          { label: "Blockers", value: toNumber(overview.inbox_counts && overview.inbox_counts.blockers) },
        ],
      ),
    );

    const footer = document.createElement("p");
    footer.className = "dashboard-footnote";
    footer.textContent = "Project: " + project;
    content.appendChild(footer);
  }

  function createStatSection(title, description, stats) {
    const section = document.createElement("section");
    section.className = "dashboard-section";

    const heading = document.createElement("h2");
    heading.textContent = title;
    section.appendChild(heading);

    const desc = document.createElement("p");
    desc.className = "section-subtitle";
    desc.textContent = description;
    section.appendChild(desc);

    const grid = document.createElement("div");
    grid.className = "stats-grid";
    section.appendChild(grid);

    const values = stats && stats.length ? stats : [{ label: "No data", value: "-" }];
    values.forEach((item) => {
      const card = document.createElement("div");
      card.className = "stat-item";

      const value = document.createElement("span");
      value.className = "stat-value";
      value.textContent = String(valueOrDash(item.value));
      card.appendChild(value);

      const label = document.createElement("span");
      label.className = "stat-label";
      label.textContent = item.label;
      card.appendChild(label);

      grid.appendChild(card);
    });
    return section;
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

  function formatTimestamp(value) {
    if (!value) {
      return "-";
    }
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return String(value);
    }
    return date.toLocaleString();
  }

  window.addEventListener("hashchange", onRouteChange);
  onRouteChange();
})();
