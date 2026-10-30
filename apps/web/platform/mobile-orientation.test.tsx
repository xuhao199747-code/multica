import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "@multica/views/locales/en/common.json";
import enSettings from "@multica/views/locales/en/settings.json";
import {
  applyPortraitLock,
  LandscapePreferenceRow,
  MobileOrientationPreferenceSync,
} from "./mobile-orientation";

const TEST_RESOURCES = {
  en: {
    common: enCommon,
    settings: {
      ...enSettings,
      preferences: {
        ...enSettings.preferences,
        orientation: {
          title: "Block landscape",
          hint: "When enabled, supported browsers and installed web apps stay in portrait.",
          rotate_title: "Rotate your phone",
          rotate_hint: "This workspace is set to stay in portrait.",
        },
      },
    },
  },
};

describe("applyPortraitLock", () => {
  it("locks the viewport to portrait when landscape is blocked", async () => {
    const lock = vi.fn().mockResolvedValue(undefined);
    const unlock = vi.fn();

    await applyPortraitLock({ lock, unlock }, true);

    expect(lock).toHaveBeenCalledWith("portrait");
    expect(unlock).not.toHaveBeenCalled();
  });

  it("returns control to the operating system when landscape is not blocked", async () => {
    const lock = vi.fn();
    const unlock = vi.fn();

    await applyPortraitLock({ lock, unlock }, false);

    expect(unlock).toHaveBeenCalledOnce();
    expect(lock).not.toHaveBeenCalled();
  });

  it("does not reject when the browser declines an orientation lock", async () => {
    const lock = vi.fn().mockRejectedValue(new Error("Not allowed"));

    await expect(
      applyPortraitLock({ lock, unlock: vi.fn() }, true),
    ).resolves.toBeUndefined();
  });
});

describe("LandscapePreferenceRow", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("persists the enabled portrait lock and requests it", async () => {
    const lock = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(window.screen, "orientation", {
      configurable: true,
      value: { lock, unlock: vi.fn() },
    });
    const user = userEvent.setup();

    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <LandscapePreferenceRow />
      </I18nProvider>,
    );

    const toggle = screen.getByRole("switch", { name: "Block landscape" });
    expect(toggle).toHaveAttribute("aria-checked", "false");

    await user.click(toggle);

    expect(localStorage.getItem("multica_lock_portrait")).toBe("true");
    expect(lock).toHaveBeenCalledWith("portrait");
  });
});

describe("MobileOrientationPreferenceSync", () => {
  const matchMedia = vi.fn();

  beforeEach(() => {
    localStorage.clear();
    matchMedia.mockReturnValue({
      matches: true,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    });
    vi.stubGlobal("matchMedia", matchMedia);
  });

  it("blocks workspace content while a portrait-locked phone is horizontal", () => {
    localStorage.setItem("multica_lock_portrait", "true");

    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MobileOrientationPreferenceSync />
      </I18nProvider>,
    );

    expect(screen.getByRole("alertdialog", { name: "Rotate your phone" })).toBeVisible();
  });
});
