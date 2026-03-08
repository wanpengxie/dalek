(function () {
  const pages = {
    overview: { title: "Overview", subtitle: "Dalek project dashboard" },
    tickets: { title: "Tickets", subtitle: "Ticket list from API" },
    "merges-inbox": {
      title: "Merges & Inbox",
      subtitle: "Merges & Inbox page",
    },
    "planner-runtime": {
      title: "Planner & Runtime",
      subtitle: "Planner & Runtime page",
    },
  };

  const ticketFilters = [
    { value: "all", label: "All" },
    { value: "backlog", label: "Backlog" },
    { value: "queued", label: "Queued" },
    { value: "active", label: "Active" },
    { value: "blocked", label: "Blocked" },
    { value: "done", label: "Done" },
    { value: "archived", label: "Archived" },
  ];

  const priorityLabels = {
    1: "Low",
    2: "Medium",
    3: "High",
  };

  let renderToken = 0;

  function parseRoute(hash) {
    const raw = (hash || "").replace(/^#\/?/, "").trim().toLowerCase();
    if (!raw) {
      return { name: "overview", canonical: "overview" };
    }

    const path = raw.split("?")[0].replace(/^\/+|\/+$/g, "");
    if (!path) {
      return { name: "overview", canonical: "overview" };
    }

    const segments = path.split("/").filter(Boolean);
    if (
      segments.length === 2 &&
      segments[0] === "tickets" &&
      /^[0-9]+$/.test(segments[1])
    ) {
      const ticketID = String(Number.parseInt(segments[1], 10));
      return {
        name: "ticket-detail",
        canonical: "tickets/" + ticketID,
        ticketID,
      };
    }

    if (segments.length === 1 && pages[segments[0]]) {
      return { name: segments[0], canonical: segments[0] };
    }

    return { name: "overview", canonical: "overview" };
  }

  function ensurePageLayout() {
    const card = document.querySelector(".page-card");
    if (!card) {
      return null;
    }

    let title = card.querySelector("#page-title");
    let subtitle = card.querySelector("#page-subtitle");
    let body = card.querySelector("#page-body");

    if (!title || !subtitle || !body) {
      card.innerHTML = "";

      title = document.createElement("h1");
      title.id = "page-title";
      card.appendChild(title);

      subtitle = document.createElement("p");
      subtitle.id = "page-subtitle";
      card.appendChild(subtitle);

      body = document.createElement("div");
      body.id = "page-body";
      body.className = "page-body";
      card.appendChild(body);
    }

    return { title, subtitle, body };
  }

  function getPageMeta(route) {
    if (route.name === "ticket-detail") {
      return {
        title: "Ticket #" + route.ticketID,
        subtitle: "Ticket detail from API",
      };
    }
    return pages[route.name] || pages.overview;
  }

  function setNavActive(routeName) {
    const navRoute = routeName === "ticket-detail" ? "tickets" : routeName;
    const navLinks = document.querySelectorAll("nav a[data-route]");
    navLinks.forEach((node) => {
      const isActive = node.getAttribute("data-route") === navRoute;
      node.classList.toggle("active", isActive);
      node.setAttribute("aria-current", isActive ? "page" : "false");
    });
  }

  function setSubtitle(text) {
    const subtitle = document.getElementById("page-subtitle");
    if (subtitle) {
      subtitle.textContent = text;
    }
  }

  function isStale(token) {
    return token !== renderToken;
  }

  function onRouteChange() {
    const route = parseRoute(window.location.hash);
    const canonicalHash = "#/" + route.canonical;
    if (window.location.hash !== canonicalHash) {
      window.location.hash = canonicalHash;
      return;
    }
    render(route);
  }

  function render(route) {
    const layout = ensurePageLayout();
    if (!layout) {
      return;
    }

    const token = ++renderToken;
    const page = getPageMeta(route);
    layout.title.textContent = page.title;
    layout.subtitle.textContent = page.subtitle;
    setNavActive(route.name);

    switch (route.name) {
      case "overview":
        renderOverview(layout.body);
        break;
      case "tickets":
        renderTicketList(layout.body, token);
        break;
      case "ticket-detail":
        renderTicketDetail(layout.body, token, route.ticketID);
        break;
      case "merges-inbox":
      case "planner-runtime":
        renderPlaceholder(layout.body, page.title);
        break;
      default:
        renderOverview(layout.body);
        break;
    }
  }

  function renderOverview(container) {
    container.innerHTML =
      '<div class="page-state info">Use the navigation to open console modules.</div>';
  }

  function renderPlaceholder(container, pageTitle) {
    container.innerHTML =
      '<div class="page-state info">' +
      escapeHTML(pageTitle) +
      " is not implemented yet.</div>";
  }

  function renderPageState(container, kind, text) {
    container.innerHTML =
      '<div class="page-state ' +
      escapeHTML(kind) +
      '">' +
      escapeHTML(text) +
      "</div>";
  }

  function getProjectName() {
    const params = new URLSearchParams(window.location.search || "");
    return (params.get("project") || "").trim();
  }

  function buildAPIPath(pathname, params) {
    const url = new URL(pathname, window.location.origin);
    Object.keys(params).forEach((key) => {
      const value = params[key];
      if (value === undefined || value === null || String(value).trim() === "") {
        return;
      }
      url.searchParams.set(key, String(value).trim());
    });
    return url.pathname + url.search;
  }

  async function fetchJSON(pathname) {
    const response = await fetch(pathname, {
      method: "GET",
      headers: { Accept: "application/json" },
    });

    let payload = null;
    try {
      payload = await response.json();
    } catch (_error) {
      payload = null;
    }

    if (!response.ok) {
      const message =
        payload && typeof payload.message === "string" && payload.message.trim()
          ? payload.message.trim()
          : "Request failed (" + response.status + ")";
      throw new Error(message);
    }

    return payload || {};
  }

  function fetchTickets(projectName) {
    return fetchJSON(buildAPIPath("/api/v1/tickets", { project: projectName }));
  }

  function fetchTicketDetail(projectName, ticketID) {
    return fetchJSON(
      buildAPIPath("/api/v1/tickets/" + encodeURIComponent(ticketID), {
        project: projectName,
      })
    );
  }

  async function renderTicketList(container, token) {
    const projectName = getProjectName();
    if (!projectName) {
      renderPageState(
        container,
        "error",
        'Missing project query parameter. Example: ?project=demo'
      );
      return;
    }

    setSubtitle("Tickets in project " + projectName);
    renderPageState(container, "loading", "Loading tickets...");

    try {
      const payload = await fetchTickets(projectName);
      if (isStale(token)) {
        return;
      }

      const tickets = Array.isArray(payload.tickets) ? payload.tickets : [];
      mountTicketList(container, tickets);
    } catch (error) {
      if (isStale(token)) {
        return;
      }
      renderPageState(container, "error", getErrorMessage(error));
    }
  }

  function mountTicketList(container, ticketViews) {
    container.innerHTML =
      '<div class="ticket-toolbar">' +
      '<div class="ticket-filters" role="tablist" aria-label="Ticket status filters">' +
      ticketFilters
        .map((filter) => {
          return (
            '<button type="button" class="ticket-filter-button" data-filter="' +
            escapeHTML(filter.value) +
            '">' +
            escapeHTML(filter.label) +
            "</button>"
          );
        })
        .join("") +
      "</div>" +
      '<div class="ticket-count" id="ticket-count"></div>' +
      "</div>" +
      '<div class="ticket-table-wrapper">' +
      '<table class="ticket-table">' +
      "<thead>" +
      "<tr>" +
      "<th>ID</th>" +
      "<th>Title</th>" +
      "<th>Status</th>" +
      "<th>Priority</th>" +
      "<th>Created</th>" +
      "</tr>" +
      "</thead>" +
      '<tbody id="ticket-table-body"></tbody>' +
      "</table>" +
      "</div>";

    const countNode = container.querySelector("#ticket-count");
    const tbody = container.querySelector("#ticket-table-body");
    const buttons = Array.from(container.querySelectorAll(".ticket-filter-button"));
    let currentFilter = "all";

    const updateList = () => {
      buttons.forEach((button) => {
        const isActive = button.getAttribute("data-filter") === currentFilter;
        button.classList.toggle("active", isActive);
        button.setAttribute("aria-selected", isActive ? "true" : "false");
      });

      const filtered = ticketViews.filter((view) => {
        if (currentFilter === "all") {
          return true;
        }
        const ticketStatus = normalizeStatus(getTicket(view).workflow_status);
        return ticketStatus === currentFilter;
      });

      if (countNode) {
        countNode.textContent = filtered.length + " / " + ticketViews.length + " tickets";
      }

      if (!tbody) {
        return;
      }

      if (!filtered.length) {
        tbody.innerHTML =
          '<tr><td colspan="5" class="ticket-empty">No tickets in this status.</td></tr>';
        return;
      }

      tbody.innerHTML = filtered
        .map((view) => renderTicketRow(view))
        .filter(Boolean)
        .join("");

      const rows = Array.from(tbody.querySelectorAll("tr[data-ticket-id]"));
      rows.forEach((row) => {
        const ticketID = row.getAttribute("data-ticket-id");
        if (!ticketID) {
          return;
        }
        const openDetail = () => {
          window.location.hash = "#/tickets/" + ticketID;
        };
        row.addEventListener("click", openDetail);
        row.addEventListener("keydown", (event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            openDetail();
          }
        });
      });
    };

    buttons.forEach((button) => {
      button.addEventListener("click", () => {
        const nextFilter = button.getAttribute("data-filter") || "all";
        currentFilter = nextFilter;
        updateList();
      });
    });

    updateList();
  }

  function renderTicketRow(view) {
    const ticket = getTicket(view);
    const ticketID = toTicketID(ticket.id);
    if (!ticketID) {
      return "";
    }

    const status = normalizeStatus(ticket.workflow_status) || "unknown";
    const createdAt = formatTimestamp(ticket.created_at);

    return (
      '<tr class="ticket-row" data-ticket-id="' +
      escapeHTML(ticketID) +
      '" tabindex="0">' +
      '<td class="mono">#' +
      escapeHTML(ticketID) +
      "</td>" +
      "<td>" +
      escapeHTML(valueOrDash(ticket.title)) +
      "</td>" +
      "<td>" +
      renderStatusBadge(status) +
      "</td>" +
      "<td>" +
      escapeHTML(formatPriority(ticket.priority)) +
      "</td>" +
      "<td>" +
      escapeHTML(createdAt) +
      "</td>" +
      "</tr>"
    );
  }

  async function renderTicketDetail(container, token, ticketID) {
    const projectName = getProjectName();
    if (!projectName) {
      renderPageState(
        container,
        "error",
        'Missing project query parameter. Example: ?project=demo'
      );
      return;
    }

    setSubtitle("Ticket detail in project " + projectName);
    renderPageState(container, "loading", "Loading ticket #" + ticketID + "...");

    try {
      const payload = await fetchTicketDetail(projectName, ticketID);
      if (isStale(token)) {
        return;
      }

      const view = payload && payload.ticket ? payload.ticket : null;
      if (!view || typeof view !== "object") {
        renderPageState(container, "error", "Ticket payload is empty.");
        return;
      }

      mountTicketDetail(container, view);
    } catch (error) {
      if (isStale(token)) {
        return;
      }
      renderPageState(container, "error", getErrorMessage(error));
    }
  }

  function mountTicketDetail(container, view) {
    const ticket = getTicket(view);
    const worker =
      view.latest_worker && typeof view.latest_worker === "object"
        ? view.latest_worker
        : null;
    const capability =
      view.capability && typeof view.capability === "object" ? view.capability : {};

    container.innerHTML =
      '<div class="ticket-detail-header">' +
      '<a class="back-link" href="#/tickets">Back to tickets</a>' +
      '<span class="ticket-detail-id mono">#' +
      escapeHTML(toTicketID(ticket.id) || "-") +
      "</span>" +
      "</div>" +
      '<div class="detail-sections">' +
      renderDetailSection("Ticket", [
        detailField("ID", toTicketID(ticket.id), { mono: true }),
        detailField("Title", valueOrDash(ticket.title)),
        detailField("Description", valueOrDash(ticket.description)),
        detailField("Label", valueOrDash(ticket.label)),
        detailField("Workflow Status", renderStatusBadge(ticket.workflow_status), {
          html: true,
        }),
        detailField("Priority", formatPriority(ticket.priority)),
        detailField("Created At", formatTimestamp(ticket.created_at)),
        detailField("Updated At", formatTimestamp(ticket.updated_at)),
        detailField("Derived Status", renderStatusBadge(view.derived_status), {
          html: true,
        }),
      ]) +
      renderDetailSection("Worker", [
        detailField("Worker ID", worker ? valueOrDash(worker.id) : "-", { mono: true }),
        detailField("Status", worker ? valueOrDash(worker.status) : "-"),
        detailField("Branch", worker ? valueOrDash(worker.branch) : "-"),
        detailField("Log Path", worker ? valueOrDash(worker.log_path) : "-", {
          mono: true,
        }),
        detailField("Last Error", worker ? valueOrDash(worker.last_error) : "-"),
        detailField("Started At", worker ? formatTimestamp(worker.started_at) : "-"),
        detailField("Stopped At", worker ? formatTimestamp(worker.stopped_at) : "-"),
        detailField("Created At", worker ? formatTimestamp(worker.created_at) : "-"),
        detailField("Updated At", worker ? formatTimestamp(worker.updated_at) : "-"),
      ]) +
      renderDetailSection("Runtime", [
        detailField("Health State", valueOrDash(view.runtime_health_state)),
        detailField("Needs User", formatBoolean(view.runtime_needs_user)),
        detailField("Summary", valueOrDash(view.runtime_summary)),
        detailField("Observed At", formatTimestamp(view.runtime_observed_at)),
        detailField("Session Alive", formatBoolean(view.session_alive)),
        detailField("Session Probe Failed", formatBoolean(view.session_probe_failed)),
        detailField("Task Run ID", valueOrDash(view.task_run_id), { mono: true }),
      ]) +
      renderDetailSection("Semantic", [
        detailField("Phase", valueOrDash(view.semantic_phase)),
        detailField("Next Action", valueOrDash(view.semantic_next_action)),
        detailField("Summary", valueOrDash(view.semantic_summary)),
        detailField("Reported At", formatTimestamp(view.semantic_reported_at)),
      ]) +
      renderDetailSection("Last Event", [
        detailField("Type", valueOrDash(view.last_event_type)),
        detailField("Note", valueOrDash(view.last_event_note)),
        detailField("At", formatTimestamp(view.last_event_at)),
      ]) +
      renderDetailSection("Capability", [
        detailField("Can Start", formatBoolean(capability.can_start)),
        detailField("Can Dispatch", formatBoolean(capability.can_dispatch)),
        detailField("Can Attach", formatBoolean(capability.can_attach)),
        detailField("Can Stop", formatBoolean(capability.can_stop)),
        detailField("Can Archive", formatBoolean(capability.can_archive)),
        detailField("Reason", valueOrDash(capability.reason)),
      ]) +
      "</div>";
  }

  function renderDetailSection(title, fields) {
    return (
      '<section class="detail-section">' +
      "<h3>" +
      escapeHTML(title) +
      "</h3>" +
      '<dl class="detail-grid">' +
      fields.join("") +
      "</dl>" +
      "</section>"
    );
  }

  function detailField(label, value, options) {
    const opts = options || {};
    const className = opts.mono ? "detail-value mono" : "detail-value";
    const content = opts.html ? valueOrDashHTML(value) : escapeHTML(valueOrDash(value));
    return (
      '<div class="detail-field">' +
      '<dt class="detail-label">' +
      escapeHTML(label) +
      "</dt>" +
      '<dd class="' +
      className +
      '">' +
      content +
      "</dd>" +
      "</div>"
    );
  }

  function valueOrDashHTML(html) {
    if (html === null || html === undefined || html === "") {
      return "-";
    }
    return String(html);
  }

  function getTicket(view) {
    if (view && typeof view === "object" && view.ticket && typeof view.ticket === "object") {
      return view.ticket;
    }
    return {};
  }

  function toTicketID(value) {
    const numeric = Number(value);
    if (!Number.isFinite(numeric) || numeric <= 0) {
      return "";
    }
    return String(Math.floor(numeric));
  }

  function normalizeStatus(status) {
    const value = String(status || "").trim().toLowerCase();
    if (!value) {
      return "";
    }
    switch (value) {
      case "backlog":
      case "queued":
      case "active":
      case "blocked":
      case "done":
      case "archived":
        return value;
      default:
        return "unknown";
    }
  }

  function renderStatusBadge(status) {
    const normalized = normalizeStatus(status) || "unknown";
    const label = normalized.charAt(0).toUpperCase() + normalized.slice(1);
    return (
      '<span class="status-badge is-' +
      escapeHTML(normalized) +
      '">' +
      escapeHTML(label) +
      "</span>"
    );
  }

  function formatPriority(priority) {
    const key = Number(priority);
    if (!Number.isFinite(key) || key <= 0) {
      return "-";
    }
    return priorityLabels[key] || "P" + key;
  }

  function formatBoolean(value) {
    if (value === true) {
      return "Yes";
    }
    if (value === false) {
      return "No";
    }
    return "-";
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

  function valueOrDash(value) {
    if (value === null || value === undefined) {
      return "-";
    }
    if (typeof value === "string") {
      const trimmed = value.trim();
      return trimmed === "" ? "-" : trimmed;
    }
    if (typeof value === "number") {
      return Number.isFinite(value) ? String(value) : "-";
    }
    if (typeof value === "boolean") {
      return value ? "true" : "false";
    }
    return String(value);
  }

  function getErrorMessage(error) {
    if (error && typeof error.message === "string" && error.message.trim()) {
      return error.message.trim();
    }
    return "Unexpected error while loading data.";
  }

  function escapeHTML(raw) {
    return String(raw)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/\"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  window.addEventListener("hashchange", onRouteChange);
  onRouteChange();
})();
