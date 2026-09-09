package api

// Test-only module accessors. They replace the former production Router
// thin wrappers (veloxModule / integrationsModule + forwarders) that were
// deleted as part of the audit dead-code remediation: the wrappers existed
// "only for test compatibility" (see the removed TODO in modules_velox.go /
// modules_integrations.go / router.go) and kept two dep-mapping sites alive
// in the production binary.
//
// Production code MUST use the typed module constructors directly (see
// routes.go::registerRoutes). Tests that build Router struct literals and
// exercise the module handlers use the accessors below — defined in a
// _test.go file so the dep-mapping cannot leak back into production.

// testVeloxModule builds a VeloxModule from the Router's current fields,
// mirroring the deps the deleted production wrapper passed (including the
// optional GroupStore for the resolve-target route).
func (r *Router) testVeloxModule() *VeloxModule {
	return NewVeloxModule(VeloxModuleDeps{
		ExternalDestinationStore: r.externalDestinations,
		ExternalDeliveryStore:    r.externalDeliveries,
		WorkspaceStore:           r.workspaceStore,
		UserStore:                r.userRepo,
		GroupStore:               r.groupStore,
		VeloxAPIToken:            r.veloxAPIToken,
		VeloxValidateRateLimiter: r.veloxValidateRateLimiter,
	}).(*VeloxModule)
}

// testIntegrationsModule builds an IntegrationsModule from the Router's
// current fields, mirroring the deps the deleted production wrapper passed.
func (r *Router) testIntegrationsModule() *IntegrationsModule {
	return NewIntegrationsModule(IntegrationsModuleDeps{
		ExternalDestinationStore: r.externalDestinations,
		WorkspaceStore:           r.workspaceStore,
		UserStore:                r.userRepo,
		AuditLogStore:            r.auditLogStore,
		AuthMiddleware:           r.authMiddleware,
		CSRFMiddleware:           r.csrfMiddleware,
	}).(*IntegrationsModule)
}
