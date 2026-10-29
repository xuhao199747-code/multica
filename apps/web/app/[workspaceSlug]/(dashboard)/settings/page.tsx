import { SettingsPage } from "@multica/views/settings";
import { LandscapePreferenceRow } from "@/platform/mobile-orientation";

export default function Page() {
  return <SettingsPage appearanceRows={<LandscapePreferenceRow />} />;
}
