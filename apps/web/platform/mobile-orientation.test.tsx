import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "@multica/views/locales/en/common.json";
import enSettings from "@multica/views/locales/en/settings.json";
import {
  applyLandscapePreference,
  LandscapePreferenceRow,
} from "./mobile-orientation";

const TEST_RESOURCES = {
  en: { common: enCommon, settings: enSettings },
};

describe("applyLandscapePreference", () => {
  it("locks the viewport to portrait when landscape is disabled", async () => {
    const lock = vi.fn().mockResolvedValue(undefined);
    const unlock = vi.fn();

    await applyLandscapePreference({ lock, unlock }, false);

    expect(lock).toHaveBeenCalledWith("portrait");
    expect(unlock).not.toHaveBeenCalled();
  });

  it("returns control to the operating system when landscape is enabled", async () => {
    const lock = vi.fn();
    const unlock = vi.fn();

    await applyLandscapePreference({ lock, unlock }, true);

    expect(unlock).toHaveBeenCalledOnce();
    expect(lock).not.toHaveBeenCalled();
  });

  it("does not reject when the browser declines an orientation lock", async () => {
    const lock = vi.fn().mockRejectedValue(new Error("Not allowed"));

    await expect(
      applyLandscapePreference({ lock, unlock: vi.fn() }, false),
    ).resolves.toBeUndefined();
  });
});

describe("LandscapePreferenceRow", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("persists the disabled preference and requests a portrait lock", async () => {
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

    const toggle = screen.getByRole("switch", { name: "Allow landscape" });
    expect(toggle).toHaveAttribute("aria-checked", "true");

    await user.click(toggle);

    expect(localStorage.getItem("multica_allow_landscape")).toBe("false");
    expect(lock).toHaveBeenCalledWith("portrait");
  });
});
