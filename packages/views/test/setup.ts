import "@testing-library/jest-dom/vitest";

function createMemoryStorage(): Storage {
  const values = new Map<string, string>();

  return {
    get length() {
      return values.size;
    },
    clear: () => values.clear(),
    getItem: (key: string) => values.get(key) ?? null,
    key: (index: number) => Array.from(values.keys())[index] ?? null,
    removeItem: (key: string) => {
      values.delete(key);
    },
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
  };
}

// Everything below patches gaps in jsdom. Pure-logic suites opt out of jsdom
// with `// @vitest-environment node` and share this file, so there is no DOM to
// patch there — bail out rather than guard each stub.
if (typeof window !== "undefined") {
  if (typeof globalThis.localStorage?.clear !== "function") {
    const storage = createMemoryStorage();
    Object.defineProperty(globalThis, "localStorage", {
      configurable: true,
      value: storage,
    });
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: storage,
    });
  }

  // jsdom doesn't provide matchMedia; useIsMobile() relies on it.
  if (typeof window.matchMedia !== "function") {
    window.matchMedia = (query: string) =>
      ({
        matches: false,
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }) as MediaQueryList;
  }

  // jsdom doesn't provide ResizeObserver; stub it so components that rely on it
  // (e.g. input-otp) can render in tests.
  if (typeof globalThis.ResizeObserver === "undefined") {
    globalThis.ResizeObserver = class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
  }

  // jsdom doesn't implement elementFromPoint; input-otp uses it internally,
  // and Base UI menus use it to resolve which menu item a pointer event
  // targets (a null stub silently swallows every menu item click). Without
  // layout we can't hit-test coordinates, so return the deepest element —
  // enough for both consumers, which only check non-null.
  if (typeof document.elementFromPoint !== "function") {
    document.elementFromPoint = () => {
      const all = document.querySelectorAll<HTMLElement>("body *");
      return all.length > 0 ? (all[all.length - 1] ?? null) : null;
    };
  }

  // jsdom has no layout, so it doesn't implement scrollIntoView; list components
  // that keep a keyboard cursor in view (e.g. the thread navigator) call it.
  if (typeof Element.prototype.scrollIntoView !== "function") {
    Element.prototype.scrollIntoView = () => {};
  }

  // jsdom lacks PointerEvent and pointer capture; Base UI menus (DropdownMenu,
  // Select, ContextMenu) gate their item clicks on capturing the originating
  // pointer, so without these every menu item click silently no-ops.
  if (typeof window.PointerEvent === "undefined") {
    class PointerEventPolyfill extends MouseEvent {
      pointerId: number;
      pointerType: string;
      isPrimary: boolean;
      constructor(type: string, params: PointerEventInit = {}) {
        super(type, params);
        this.pointerId = params.pointerId ?? 1;
        this.pointerType = params.pointerType ?? "mouse";
        this.isPrimary = params.isPrimary ?? true;
      }
    }
    Object.defineProperty(window, "PointerEvent", {
      configurable: true,
      writable: true,
      value: PointerEventPolyfill,
    });
    // Some code paths read the constructor from the global scope rather than
    // window (e.g. `instanceof PointerEvent` inside event handlers).
    Object.defineProperty(globalThis, "PointerEvent", {
      configurable: true,
      writable: true,
      value: PointerEventPolyfill,
    });
  }
  if (typeof Element.prototype.setPointerCapture !== "function") {
    Element.prototype.setPointerCapture = () => {};
    Element.prototype.releasePointerCapture = () => {};
    Element.prototype.hasPointerCapture = () => false;
  }
}
