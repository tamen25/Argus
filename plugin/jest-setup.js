// Jest setup provided by Grafana scaffolding
import './.config/jest-setup';

// @grafana/scenes uses IntersectionObserver (lazy activation); jsdom has none.
class IntersectionObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
  takeRecords() {
    return [];
  }
}
global.IntersectionObserver = global.IntersectionObserver || IntersectionObserverStub;

// @grafana/ui text measurement (Combobox, BigValue) needs a canvas context;
// jsdom returns null from getContext.
if (typeof HTMLCanvasElement !== 'undefined') {
  HTMLCanvasElement.prototype.getContext = function () {
    return {
      measureText: (text) => ({ width: String(text).length * 8 }),
      font: '',
    };
  };
}
