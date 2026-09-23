# InstaEdit control-plane agent readiness

Scope: this checklist applies only to the `InstaeditLogin` control plane (the
51). PipelineGen/RenderingGen worker changes are explicitly out of scope.

## Acceptance checklist

- [x] Keep the provider-neutral Job Master client server-side and configurable
      through `JOB_MASTER_*`; no worker IP is hardcoded in application code.
- [x] Keep the authenticated BFF lifecycle routes for remote job types,
      submit, and polling.
- [x] Add one control-plane Automation Catalog and derive the agent tool list
      from it.
- [x] Expose only external-safe, catalogued tools to agents; keep internal
      worker job types out of the agent surface.
- [x] Add a typed agent tool endpoint so agents do not submit arbitrary raw
      remote job JSON.
- [x] Make agent run reads and step mutations workspace-owned.
- [x] Make run idempotency reject the same key with a different request.
- [x] Persist remote job id and idempotency key on each agent step.
- [x] Add recovery reads for runs with non-terminal remote steps.
- [x] Complete the first script-only vertical slice with persisted progress:
      create run → `content.generate_script` → poll/recovery → terminal status.
- [x] Persist intermediate remote status, percentage and generic stage
      snapshots when recovery is polled.
- [x] Add automatic server-side polling so progress is persisted without an
      agent repeatedly calling recovery; worker is cancellable with its context.
- [x] Add a control-plane `content.create_video` workflow plan.
- [ ] Execute the durable multi-step plan with dependency/result handoff,
      restart-safe scheduling, and failure/retry policy. Current remote catalog
      does not advertise every required video capability.
- [x] Connect Calendar's single-video flow to durable run/recovery, Media
      Library import, normal scheduled post publication, 30-day navigation and
      final artifact preview/link on the event card.
- [x] Expose the next 30 days in a rolling Calendar view; scheduled posts can
      be rescheduled by drag, started immediately or cancelled from card detail.
- [x] Persist an individual scheduled generation intent as a Calendar draft
      with a workspace-unique idempotency key; dispatch it from the recovery
      worker and support run-now, reschedule and cancel.
- [x] Add topic search against PipelineGen's read-only media catalog and build
      a scene-composite PREPARE manifest from matching ready Drive-backed clips.
- [ ] Add bulk creation of daily calendar intents. Single intents now store
      their IANA timezone and publish instant, then dispatch 30 minutes before
      publication.
- [x] Project remote current stage, stage progress, timeline and events on the
      Calendar detail card. Snapshot extraction retains fields from the Job
      Master's outer response envelope and bounds persisted event history.
- [ ] Make the execution plane emit and complete every required generation
      phase. The Calendar can display only phases the remote job actually emits.
- [ ] Connect script/voiceover/stock/extraction/overlay/thumbnail jobs into the
      parent workflow; current render consumes registered video clips and the
      live M2M catalog has no thumbnail or final-audio job.
- [x] Add focused unit/HTTP/repository tests for all new contracts.
- [x] Update the control-plane architecture and M2M documentation.
- [x] Run formatter, focused tests, full Go tests, frontend tests/build, and
      confirm no generated artifacts or diff errors in the previous agent run.
- [x] Re-run verification after the current idempotency/recovery fixes.
- [ ] Verify migration 136 and authenticated calendar intent flow against the
      running database/API.

## Explicitly deferred to the execution plane

- Implementing `video.create`, `video.assemble`, stock, clip extraction,
  rendering, and worker registration inside PipelineGen.
- Changing RenderingGen/Chronon or exposing the RenderingGen queue to agents.
- Deploying or modifying the remote worker 77.
