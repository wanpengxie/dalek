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

  function normalizeRoute(hash) {
    const raw = (hash || "").replace(/^#\/?/, "").trim().toLowerCase();
    if (raw && pages[raw]) {
      return raw;
    }
    return "overview";
  }

  function render(route) {
    const page = pages[route];
    const title = document.getElementById("page-title");
    const subtitle = document.getElementById("page-subtitle");
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
