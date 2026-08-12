import { apiClient } from "../../lib/api-client";

export type CoverLibraryItem = {
  export_id: string;
  workspace_id: number;
  project_id: string;
  project_name: string;
  revision_id: string;
  media_id: string;
  content_type: string;
  width: number;
  height: number;
  file_size: number;
  sha256: string;
  renderer_version: string;
  render_profile?: string;
  status: string;
  created_at: string;
  updated_at: string;
};

export type CoverTemplate = {
  id: number;
  workspace_id: number;
  created_by: number;
  name: string;
  description: string;
  category?: string;
  language?: string;
  status: "active" | "archived" | string;
  current_version_number: number;
  created_at: string;
  updated_at: string;
};

export type CoverTemplateVersion = {
  id: number;
  template_id: number;
  version_number: number;
  editor_project_id: string;
  preview_media_id?: string;
  slots: Record<string, unknown>;
  created_by: number;
  created_at: string;
};

export function listCoverLibrary(workspaceId: number, status = "") {
  const params = new URLSearchParams({ workspace_id: String(workspaceId) });
  if (status) params.set("status", status);
  return apiClient<{ items: CoverLibraryItem[] }>(`/api/v1/cover-library?${params}`);
}

export function listCoverTemplates(workspaceId: number, language = "", status = "active") {
  const params = new URLSearchParams({ workspace_id: String(workspaceId) });
  if (language) params.set("language", language);
  if (status) params.set("status", status);
  return apiClient<{ items: CoverTemplate[] }>(`/api/v1/template-library?${params}`);
}

export function listCoverTemplateVersions(workspaceId: number, templateId: number) {
  return apiClient<{ items: CoverTemplateVersion[] }>(
    `/api/v1/template-library/${templateId}/versions?workspace_id=${workspaceId}`,
  );
}

export function createCoverTemplate(
  input: {
    workspace_id: number;
    name: string;
    description?: string;
    category?: string;
    language?: string;
    editor_project_id: string;
    preview_media_id?: string;
    slots?: Record<string, unknown>;
  },
) {
  return apiClient<{ template: CoverTemplate; version: CoverTemplateVersion }>(
    "/api/v1/template-library",
    { method: "POST", body: input },
  );
}

export function createCoverTemplateVersion(
  workspaceId: number,
  templateId: number,
  input: { editor_project_id: string; preview_media_id?: string; slots?: Record<string, unknown> },
) {
  return apiClient<CoverTemplateVersion>(
    `/api/v1/template-library/${templateId}/versions?workspace_id=${workspaceId}`,
    { method: "POST", body: input },
  );
}

export function replaceContentPackageTargets(
  packageId: string,
  input: {
    expected_package_version: number;
    targets: Array<{
      platform_account_id: number;
      language: string;
      privacy_status: string;
      playlist_id?: string;
      enabled: boolean;
      cover_media_id?: string;
      cover_template_version_id?: number;
    }>;
  },
) {
  return apiClient<{ package: { version: number }; targets: unknown[] }>(
    `/api/v1/content-packages/${packageId}/targets`,
    { method: "PUT", body: input },
  );
}
