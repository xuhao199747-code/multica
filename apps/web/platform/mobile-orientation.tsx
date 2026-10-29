"use client";

import { useCallback, useEffect, useSyncExternalStore } from "react";
import { Switch } from "@multica/ui/components/ui/switch";
import { SettingsRow } from "@multica/views/settings";
import { useT } from "@multica/views/i18n";

const STORAGE_KEY = "multica_lock_portrait";
const CHANGE_EVENT = "multica:landscape-preference";
const DEFAULT_LOCK_PORTRAIT = false;

export interface ScreenOrientationController {
  lock(orientation: "portrait" | "landscape"): Promise<void>;
  unlock(): void;
}

function getStoredPreference(): boolean {
  if (typeof window === "undefined") return DEFAULT_LOCK_PORTRAIT;
  try {
    return window.localStorage.getItem(STORAGE_KEY) === "true";
  } catch {
    return DEFAULT_LOCK_PORTRAIT;
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
 * Chrome permits orientation locks for installed web apps. Keep the saved
 * preference when another browser rejects the request so it can apply later.
 */
export async function applyPortraitLock(
  orientation: ScreenOrientationController | null,
  lockPortrait: boolean,
): Promise<void> {
  if (!orientation) return;
  try {
    if (lockPortrait) {
      await orientation.lock("portrait");
    } else {
      orientation.unlock();
    }
  } catch {
    // Some mobile browsers expose the API but only allow locking in fullscreen.
  }
}

function usePortraitLockPreference() {
  const lockPortrait = useSyncExternalStore(
    subscribe,
    getStoredPreference,
    () => DEFAULT_LOCK_PORTRAIT,
  );
  const setLockPortrait = useCallback((next: boolean) => {
    try {
      window.localStorage.setItem(STORAGE_KEY, String(next));
    } catch {
      // A private browsing policy can block storage without blocking rotation.
    }
    void applyPortraitLock(getScreenOrientation(), next);
    window.dispatchEvent(new Event(CHANGE_EVENT));
  }, []);

  return { lockPortrait, setLockPortrait };
}

/** Applies a previously saved portrait lock as soon as the web app loads. */
export function MobileOrientationPreferenceSync() {
  const { lockPortrait } = usePortraitLockPreference();

  useEffect(() => {
    void applyPortraitLock(getScreenOrientation(), lockPortrait);
  }, [lockPortrait]);

  return null;
}

/** Web-only setting: desktop has no meaningful browser orientation to manage. */
export function LandscapePreferenceRow() {
  const { t } = useT("settings");
  const { lockPortrait, setLockPortrait } = usePortraitLockPreference();
  const label = t(($) => $.preferences.orientation.title);

  return (
    <SettingsRow
      anchor="orientation"
      label={label}
      description={t(($) => $.preferences.orientation.hint)}
    >
      <Switch
        checked={lockPortrait}
        onCheckedChange={setLockPortrait}
        aria-label={label}
      />
    </SettingsRow>
  );
}
