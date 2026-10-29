"use client";

import { useCallback, useEffect, useSyncExternalStore } from "react";
import { Switch } from "@multica/ui/components/ui/switch";
import { SettingsRow } from "@multica/views/settings";
import { useT } from "@multica/views/i18n";

const STORAGE_KEY = "multica_allow_landscape";
const CHANGE_EVENT = "multica:landscape-preference";
const DEFAULT_ALLOW_LANDSCAPE = true;

export interface ScreenOrientationController {
  lock(orientation: "portrait" | "landscape"): Promise<void>;
  unlock(): void;
}

function getStoredPreference(): boolean {
  if (typeof window === "undefined") return DEFAULT_ALLOW_LANDSCAPE;
  try {
    return window.localStorage.getItem(STORAGE_KEY) !== "false";
  } catch {
    return DEFAULT_ALLOW_LANDSCAPE;
  }
}

function subscribe(onStoreChange: () => void) {
  if (typeof window === "undefined") return () => {};
  window.addEventListener(CHANGE_EVENT, onStoreChange);
  window.addEventListener("storage", onStoreChange);
  return () => {
    window.removeEventListener(CHANGE_EVENT, onStoreChange);
    window.removeEventListener("storage", onStoreChange);
  };
}

function getScreenOrientation(): ScreenOrientationController | null {
  if (typeof window === "undefined") return null;
  const orientation = (window.screen as Screen & {
    orientation?: ScreenOrientationController;
  }).orientation;
  return orientation ?? null;
}

/**
 * The browser may require fullscreen or a user gesture before accepting a
 * screen lock. Keep the saved preference even when that restriction applies;
 * the next eligible page load can apply it.
 */
export async function applyLandscapePreference(
  orientation: ScreenOrientationController | null,
  allowLandscape: boolean,
): Promise<void> {
  if (!orientation) return;
  try {
    if (allowLandscape) {
      orientation.unlock();
    } else {
      await orientation.lock("portrait");
    }
  } catch {
    // Some mobile browsers expose the API but only allow locking in fullscreen.
  }
}

function useLandscapePreference() {
  const allowLandscape = useSyncExternalStore(
    subscribe,
    getStoredPreference,
    () => DEFAULT_ALLOW_LANDSCAPE,
  );
  const setAllowLandscape = useCallback((next: boolean) => {
    try {
      window.localStorage.setItem(STORAGE_KEY, String(next));
    } catch {
      // A private browsing policy can block storage without blocking rotation.
    }
    void applyLandscapePreference(getScreenOrientation(), next);
    window.dispatchEvent(new Event(CHANGE_EVENT));
  }, []);

  return { allowLandscape, setAllowLandscape };
}

/** Applies a previously saved portrait lock as soon as the web app loads. */
export function MobileOrientationPreferenceSync() {
  const { allowLandscape } = useLandscapePreference();

  useEffect(() => {
    void applyLandscapePreference(getScreenOrientation(), allowLandscape);
  }, [allowLandscape]);

  return null;
}

/** Web-only setting: desktop has no meaningful browser orientation to manage. */
export function LandscapePreferenceRow() {
  const { t } = useT("settings");
  const { allowLandscape, setAllowLandscape } = useLandscapePreference();
  const label = t(($) => $.preferences.orientation.title);

  return (
    <SettingsRow
      anchor="orientation"
      label={label}
      description={t(($) => $.preferences.orientation.hint)}
    >
      <Switch
        checked={allowLandscape}
        onCheckedChange={setAllowLandscape}
        aria-label={label}
      />
    </SettingsRow>
  );
}
