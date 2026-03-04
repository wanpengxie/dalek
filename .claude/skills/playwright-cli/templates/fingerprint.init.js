(() => {
  const patch = (obj, key, value) => {
    try {
      Object.defineProperty(obj, key, {
        configurable: true,
        enumerable: true,
        get: () => value
      });
    } catch {
      // Ignore if browser blocks overriding this property.
    }
  };

  patch(navigator, "webdriver", false);
  patch(navigator, "language", "zh-CN");
  patch(navigator, "languages", ["zh-CN", "zh", "en-US", "en"]);
  patch(navigator, "platform", "MacIntel");
  patch(navigator, "hardwareConcurrency", 8);
  patch(navigator, "deviceMemory", 8);

  if (!window.chrome) {
    patch(window, "chrome", {
      runtime: {},
      app: {}
    });
  }
})();
