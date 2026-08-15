import { apiClient } from "../../lib/api-client";

export type ContentPackage = {
  id: number;
  source_filename: string;
  source_language: string;
  state: string;
  version: number;
  current_cover_media_id?: string;
  drive_file_id: string;
  workspace_id: number;
};

export type ContentPackageTarget = {
  id: number;
  platform_account_id: number;
  language: string;
  privacy_status: string;
  playlist_id?: string;
  enabled: boolean;
  cover_media_id?: string;
  cover_template_version_id?: number;
};

export type ContentPackagePublication = {
  content_package_id: number;
  content_schedule_id: number;
  target_account_id: number;
  language: string;
  title: string;
  upload_job_id?: number;
  upload_job_status?: string;
  post_id?: number;
  post_target_id?: number;
  target_status?: string;
  youtube_video_id?: string;
  thumbnail_status?: string;
  published_at?: string;
};

export type ContentPackageResponse = {
  package: ContentPackage;
  targets: ContentPackageTarget[];
  publications?: ContentPackagePublication[];
  metadata?: {
    title: string;
    description: string;
    revision_number: number;
  };
  schedule?: {
    scheduled_at: string;
    prepare_at: string;
    timezone: string;
    status: string;
  };
};

export type ContentPreview = {
  package_id: number;
  package_version: number;
  ready: boolean;
  blockers: { code: string; message: string }[];
  targets: {
    platform_account_id: number;
    channel_name?: string;
    language: string;
    title: string;
    description: string;
    thumbnail_media_id?: string;
    cover_template_version_id?: number;
    privacy_status: string;
    scheduled_at?: string;
    ready: boolean;
    blockers: { code: string; message: string }[];
  }[];
  schedule?: ContentPackageResponse["schedule"];
};

export type PublicationEvent = {
  id: number;
  stage: string;
  event_type: string;
  error_code?: string;
  occurred_at: string;
};

export function getContentPackage(id: string) {
  return apiClient<ContentPackageResponse>(`/api/v1/content-packages/${id}`);
}

export function getContentPackagePreview(id: string) {
  return apiClient<ContentPreview>(`/api/v1/content-packages/${id}/preview`);
}

export function getContentPackageActivity(id: string) {
  return apiClient<{ events: PublicationEvent[] }>(
    `/api/v1/content-packages/${id}/activity`,
  );
}

export function scheduleContentPackage(
  id: string,
  input: { expected_package_version: number; scheduled_at: string; timezone: string },
) {
  return apiClient<ContentPackageResponse>(
    `/api/v1/content-packages/${id}/schedule`,
    { method: "POST", body: input },
  );
}

export function replaceContentPackageTargets(
  id: string,
  input: {
    expected_package_version: number;
    targets: Array<{
      platform_account_id: number;
      language: string;
      privacy_status: string;
      enabled: boolean;
      playlist_id?: string;
      cover_media_id?: string;
      cover_template_version_id?: number;
    }>;
  },
) {
  return apiClient<{ package: ContentPackage; targets: ContentPackageTarget[] }>(
    `/api/v1/content-packages/${id}/targets`,
    { method: "PUT", body: input },
  );
}

export function generateContentPackageTranslations(id: string, expectedVersion: number) {
  return apiClient(`/api/v1/content-packages/${id}/translations`, {
    method: "POST",
    body: { expected_package_version: expectedVersion, generate: true },
  });
}
