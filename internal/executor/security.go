package executor

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
)

func isSecurityStmt(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case ast.CreateUser, ast.DropUser, ast.CreateRole, ast.DropRole, ast.Grant, ast.Revoke:
		return true
	default:
		return false
	}
}

func (s *Session) authorize(stmt ast.Stmt) error {
	if s == nil || s.acl == nil {
		return nil
	}
	user := s.user
	switch st := stmt.(type) {
	case ast.With:
		saved := s.cteNames
		inner := copyCTENames(saved)
		s.cteNames = inner
		defer func() { s.cteNames = saved }()
		for _, cte := range st.CTEs {
			if err := s.authorize(cte.Query); err != nil {
				return err
			}
			inner[cte.Name] = struct{}{}
		}
		return s.authorize(st.Query)
	case ast.SetOperation:
		if err := s.authorize(st.Left); err != nil {
			return err
		}
		return s.authorize(st.Right)
	case ast.Select:
		if st.FromQuery != nil {
			if err := s.authorize(st.FromQuery); err != nil {
				return err
			}
		} else if !s.cteNamed(st.Table) {
			if err := s.require(security.PrivSelect, security.ScopeTable, st.Table); err != nil {
				return err
			}
		}
		for _, j := range st.Joins {
			if s.cteNamed(j.Table) {
				continue
			}
			if err := s.require(security.PrivSelect, security.ScopeTable, j.Table); err != nil {
				return err
			}
		}
		if err := s.authorizeExpr(st.Where); err != nil {
			return err
		}
		if err := s.authorizeExpr(st.Having); err != nil {
			return err
		}
		for _, item := range st.List {
			if err := s.authorizeExpr(item.Expr); err != nil {
				return err
			}
		}
		for _, j := range st.Joins {
			if err := s.authorizeExpr(j.On); err != nil {
				return err
			}
		}
		return nil
	case ast.Insert:
		if err := s.require(security.PrivInsert, security.ScopeTable, st.Table); err != nil {
			return err
		}
		return s.authorizeReturning(st.ReturningStar, st.Returning, st.Table)
	case ast.Upsert:
		if err := s.require(security.PrivInsert, security.ScopeTable, st.Table); err != nil {
			return err
		}
		if err := s.require(security.PrivUpdate, security.ScopeTable, st.Table); err != nil {
			return err
		}
		for _, a := range st.Sets {
			if err := s.authorizeExpr(a.Expr); err != nil {
				return err
			}
		}
		return s.authorizeReturning(st.ReturningStar, st.Returning, st.Table)
	case ast.Update:
		if err := s.require(security.PrivUpdate, security.ScopeTable, st.Table); err != nil {
			return err
		}
		if err := s.authorizeExpr(st.Where); err != nil {
			return err
		}
		for _, a := range st.Sets {
			if err := s.authorizeExpr(a.Expr); err != nil {
				return err
			}
		}
		return s.authorizeReturning(st.ReturningStar, st.Returning, st.Table)
	case ast.Delete:
		if err := s.require(security.PrivDelete, security.ScopeTable, st.Table); err != nil {
			return err
		}
		if err := s.authorizeExpr(st.Where); err != nil {
			return err
		}
		return s.authorizeReturning(st.ReturningStar, st.Returning, st.Table)
	case ast.CreateTable:
		return s.require(security.PrivCreate, security.ScopeDatabase, "")
	case ast.CreateWorkflow:
		return s.require(security.PrivCreate, security.ScopeDatabase, "")
	case ast.RunWorkflow:
		return s.require(security.PrivExecute, security.ScopeFunction, st.Name)
	case ast.AlterWorkflow:
		return s.require(security.PrivAlter, security.ScopeFunction, st.Name)
	case ast.DropWorkflow:
		return s.require(security.PrivDrop, security.ScopeFunction, st.Name)
	case ast.CreateTrigger:
		return s.require(security.PrivCreate, security.ScopeDatabase, "")
	case ast.AlterTrigger:
		return s.require(security.PrivAlter, security.ScopeFunction, st.Name)
	case ast.DropTrigger:
		return s.require(security.PrivDrop, security.ScopeFunction, st.Name)
	case ast.CreateSchedule:
		if err := s.require(security.PrivCreate, security.ScopeDatabase, ""); err != nil {
			return err
		}
		return s.require(security.PrivExecute, security.ScopeFunction, st.Workflow)
	case ast.AlterSchedule:
		return s.require(security.PrivAlter, security.ScopeFunction, st.Name)
	case ast.DropSchedule:
		return s.require(security.PrivDrop, security.ScopeFunction, st.Name)
	case ast.CreateResourceGroup, ast.AlterResourceGroup, ast.DropResourceGroup:
		// Workload governance is a cluster-wide admin concern, like roles and
		// users, not a per-object privilege like schedules/workflows/triggers.
		return s.require(security.PrivAdmin, security.ScopeCluster, "")
	case ast.SetResourceGroup:
		// Assignment, unlike the DDL above, is per-object: a session may only
		// switch into a resource group it was explicitly granted USAGE on
		// (GRANT USAGE ON RESOURCE GROUP name TO ...), same shape as running a
		// workflow requiring PrivExecute/ScopeFunction on that workflow. Cluster
		// ADMIN still bypasses via the superuser check inside require/Allowed.
		return s.require(security.PrivUsage, security.ScopeResourceGroup, st.Name)
	case ast.ResetResourceGroup:
		return s.require(security.PrivConnect, security.ScopeDatabase, "")
	case ast.ShowTasks, ast.CancelTask:
		return s.require(security.PrivConnect, security.ScopeDatabase, "")
	case ast.Subscribe:
		return s.require(security.PrivCDC, security.ScopeTable, st.Table)
	case ast.CreateDatabase:
		return s.require(security.PrivCreate, security.ScopeDatabase, "")
	case ast.DropTable:
		if err := s.require(security.PrivDrop, security.ScopeTable, st.Name); err != nil {
			return s.require(security.PrivDrop, security.ScopeDatabase, "")
		}
		return nil
	case ast.AlterTable:
		if err := s.require(security.PrivAlter, security.ScopeTable, st.Table); err != nil {
			if err := s.require(security.PrivAlter, security.ScopeDatabase, ""); err != nil {
				return err
			}
		}
		switch cmd := st.Cmd.(type) {
		case ast.AlterAttachPartition:
			if err := s.require(security.PrivDrop, security.ScopeTable, cmd.Partition.Name); err != nil {
				return s.require(security.PrivDrop, security.ScopeDatabase, "")
			}
		case ast.AlterDetachPartition:
			return s.require(security.PrivCreate, security.ScopeDatabase, "")
		}
		return nil
	case ast.CreateIndex:
		return s.require(security.PrivIndex, security.ScopeTable, st.Table)
	case ast.DropIndex:
		return s.require(security.PrivIndex, security.ScopeTable, st.Table)
	case ast.RebuildIndex:
		return s.require(security.PrivIndex, security.ScopeTable, st.Table)
	case ast.Analyze:
		if st.Table == "" {
			return s.require(security.PrivSelect, security.ScopeDatabase, "")
		}
		return s.require(security.PrivSelect, security.ScopeTable, st.Table)
	case ast.Maintain:
		// Maintenance rewrites physical structures across tenant boundaries.
		return s.require(security.PrivAdmin, security.ScopeCluster, "")
	case ast.TransferLeader:
		return s.require(security.PrivAdmin, security.ScopeCluster, "")
	case ast.ClusterDrain:
		return s.require(security.PrivAdmin, security.ScopeCluster, "")
	case ast.ClusterMaintenance:
		return s.require(security.PrivAdmin, security.ScopeCluster, "")
	case ast.ClusterReconcileConfirm:
		return s.require(security.PrivAdmin, security.ScopeCluster, "")
	case ast.SetConfig:
		return s.require(security.PrivAdmin, security.ScopeCluster, "")
	case ast.BackupDatabase, ast.VerifyBackup:
		// The dedicated BACKUP privilege (database scope) or cluster ADMIN.
		if s.acl == nil ||
			s.authAllowed(user, security.PrivBackup, security.ScopeDatabase, "") ||
			s.authAllowed(user, security.PrivAdmin, security.ScopeCluster, "") {
			return nil
		}
		return security.Deny("executor.authorize")
	case ast.Explain:
		return s.authorize(st.Stmt)
	case ast.Begin, ast.Commit, ast.Rollback:
		return s.require(security.PrivConnect, security.ScopeDatabase, "")
	case ast.CreateUser, ast.DropUser, ast.CreateRole, ast.DropRole:
		return s.require(security.PrivAdmin, security.ScopeCluster, "")
	case ast.Grant, ast.Revoke:
		if s.authAllowed(user, security.PrivGrant, security.ScopeCluster, "") ||
			s.authAllowed(user, security.PrivAdmin, security.ScopeCluster, "") {
			return nil
		}
		return security.Deny("executor.authorize")
	default:
		// Fail closed: a future statement type must be listed above.
		return security.Deny("executor.authorize")
	}
}

func (s *Session) authorizeReturning(star bool, list []ast.SelectItem, table string) error {
	if !star && len(list) == 0 {
		return nil
	}
	if err := s.require(security.PrivSelect, security.ScopeTable, table); err != nil {
		return err
	}
	for _, item := range list {
		if err := s.authorizeExpr(item.Expr); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) authorizeExpr(e ast.Expr) error {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case ast.ScalarSubquery:
		return s.authorize(x.Query)
	case ast.InSubquery:
		if err := s.authorizeExpr(x.Expr); err != nil {
			return err
		}
		return s.authorize(x.Query)
	case ast.ExistsSubquery:
		return s.authorize(x.Query)
	case ast.Unary:
		return s.authorizeExpr(x.Right)
	case ast.Binary:
		if err := s.authorizeExpr(x.Left); err != nil {
			return err
		}
		return s.authorizeExpr(x.Right)
	case ast.Between:
		if err := s.authorizeExpr(x.Expr); err != nil {
			return err
		}
		if err := s.authorizeExpr(x.Low); err != nil {
			return err
		}
		return s.authorizeExpr(x.High)
	case ast.IsNull:
		return s.authorizeExpr(x.Expr)
	case ast.Call:
		for _, a := range x.Args {
			if err := s.authorizeExpr(a); err != nil {
				return err
			}
		}
		return nil
	case ast.Window:
		if err := s.authorizeExpr(x.Fn); err != nil {
			return err
		}
		for _, p := range x.Partition {
			if err := s.authorizeExpr(p); err != nil {
				return err
			}
		}
		for _, o := range x.Order {
			if err := s.authorizeExpr(o.Expr); err != nil {
				return err
			}
		}
		return nil
	case ast.Case:
		if err := s.authorizeExpr(x.Operand); err != nil {
			return err
		}
		for _, arm := range x.Whens {
			if err := s.authorizeExpr(arm.When); err != nil {
				return err
			}
			if err := s.authorizeExpr(arm.Then); err != nil {
				return err
			}
		}
		return s.authorizeExpr(x.Else)
	default:
		return nil
	}
}

func (s *Session) require(priv security.Privilege, scope security.ScopeKind, object string) error {
	if s.acl == nil {
		return nil
	}
	if s.authAllowed(s.user, priv, scope, object) {
		return nil
	}
	return security.Deny("executor.authorize")
}

func (s *Session) execSecurity(stmt ast.Stmt) (*Result, error) {
	if err := s.authorize(stmt); err != nil {
		s.auditRecord(securityAction(stmt), sqlObject(stmt), err)
		return nil, err
	}
	if s.acl == nil && s.users == nil {
		return nil, nerr.New(nerr.Unavailable, "executor.execSecurity", "security catalog is not configured")
	}
	var err error
	switch st := stmt.(type) {
	case ast.CreateUser:
		if s.users == nil {
			err = nerr.New(nerr.Unavailable, "executor.CreateUser", "auth store is not configured")
			break
		}
		err = s.users.UpsertInRealm(s.realmID, st.Name, st.Password)
		if err == nil && s.acl != nil {
			err = s.acl.AddUserInRealm(s.realmID, st.Name)
		}
		s.auditRecord(security.ActionUserCreate, st.Name, err)
	case ast.DropUser:
		if s.users != nil {
			err = s.users.DeleteInRealm(s.realmID, st.Name)
		}
		if err == nil && s.acl != nil {
			err = s.acl.DropUserInRealm(s.realmID, st.Name)
		}
		if err == nil && s.registry != nil {
			s.registry.Terminate(st.Name)
		}
		s.auditRecord(security.ActionUserDrop, st.Name, err)
	case ast.CreateRole:
		if s.acl == nil {
			err = nerr.New(nerr.Unavailable, "executor.CreateRole", "ACL is not configured")
			break
		}
		err = s.acl.CreateRoleInRealm(s.realmID, st.Name)
		s.auditRecord(security.ActionRoleCreate, st.Name, err)
	case ast.DropRole:
		if s.acl == nil {
			err = nerr.New(nerr.Unavailable, "executor.DropRole", "ACL is not configured")
			break
		}
		err = s.acl.DropRoleInRealm(s.realmID, st.Name)
		s.auditRecord(security.ActionRoleDrop, st.Name, err)
	case ast.Grant:
		err = s.applyGrant(st)
		s.auditRecord(security.ActionGrant, st.Grantee, err)
	case ast.Revoke:
		err = s.applyRevoke(st)
		s.auditRecord(security.ActionRevoke, st.Grantee, err)
	default:
		err = nerr.New(nerr.Internal, "executor.execSecurity", "unsupported security statement")
	}
	if err != nil {
		return nil, err
	}
	return &Result{}, nil
}

func (s *Session) applyGrant(g ast.Grant) error {
	if s.acl == nil {
		return nerr.New(nerr.Unavailable, "executor.Grant", "ACL is not configured")
	}
	if g.Role != "" && len(g.Privileges) == 0 && !g.All {
		return s.acl.GrantRoleInRealm(s.realmID, g.Role, g.Grantee)
	}
	scope, err := security.ParseScope(g.Scope)
	if err != nil {
		return err
	}
	privs := g.Privileges
	if g.All {
		privs = []string{"admin"}
	}
	for _, name := range privs {
		priv, err := security.ParsePrivilege(name)
		if err != nil {
			return err
		}
		if err := s.acl.GrantInRealm(s.realmID, g.Grantee, priv, scope, g.Object); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) applyRevoke(g ast.Revoke) error {
	if s.acl == nil {
		return nerr.New(nerr.Unavailable, "executor.Revoke", "ACL is not configured")
	}
	if g.Role != "" && len(g.Privileges) == 0 && !g.All {
		return s.acl.RevokeRoleInRealm(s.realmID, g.Role, g.Grantee)
	}
	scope, err := security.ParseScope(g.Scope)
	if err != nil {
		return err
	}
	privs := g.Privileges
	if g.All {
		privs = []string{"admin"}
	}
	for _, name := range privs {
		priv, err := security.ParsePrivilege(name)
		if err != nil {
			return err
		}
		if err := s.acl.RevokeInRealm(s.realmID, g.Grantee, priv, scope, g.Object); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) auditRecord(action, object string, err error) {
	if s == nil || s.audit == nil {
		return
	}
	s.audit.Record(security.Event{
		Actor:   s.user,
		Action:  action,
		Object:  object,
		Outcome: security.Outcome(err),
		Remote:  s.remote,
	})
}

func securityAction(stmt ast.Stmt) string {
	switch stmt.(type) {
	case ast.CreateUser:
		return security.ActionUserCreate
	case ast.DropUser:
		return security.ActionUserDrop
	case ast.CreateRole:
		return security.ActionRoleCreate
	case ast.DropRole:
		return security.ActionRoleDrop
	case ast.Grant:
		return security.ActionGrant
	case ast.Revoke:
		return security.ActionRevoke
	default:
		return security.ActionDDL
	}
}

func sqlObject(stmt ast.Stmt) string {
	switch st := stmt.(type) {
	case ast.CreateWorkflow:
		return st.Name
	case ast.RunWorkflow:
		return st.Name
	case ast.AlterWorkflow:
		return st.Name
	case ast.DropWorkflow:
		return st.Name
	case ast.CreateTrigger:
		return st.Name
	case ast.AlterTrigger:
		return st.Name
	case ast.DropTrigger:
		return st.Name
	case ast.CreateSchedule:
		return st.Name
	case ast.AlterSchedule:
		return st.Name
	case ast.DropSchedule:
		return st.Name
	case ast.CreateResourceGroup:
		return st.Name
	case ast.AlterResourceGroup:
		return st.Name
	case ast.DropResourceGroup:
		return st.Name
	case ast.CancelTask:
		return st.ID
	case ast.Subscribe:
		return st.Table
	case ast.CreateTable:
		return st.Name
	case ast.CreateDatabase:
		return st.Name
	case ast.DropTable:
		return st.Name
	case ast.AlterTable:
		return st.Table
	case ast.CreateIndex:
		return st.Table
	case ast.DropIndex:
		return st.Table
	case ast.RebuildIndex:
		return st.Table
	case ast.Maintain:
		return st.Table
	case ast.TransferLeader:
		return ""
	case ast.ClusterDrain:
		return ""
	case ast.ClusterMaintenance:
		return ""
	case ast.ClusterReconcileConfirm:
		return ""
	case ast.SetConfig:
		return st.Key
	case ast.VerifyBackup:
		return st.Name
	case ast.BackupDatabase:
		return ""
	case ast.Select:
		return st.Table
	case ast.Insert:
		return st.Table
	case ast.Upsert:
		return st.Table
	case ast.Update:
		return st.Table
	case ast.Delete:
		return st.Table
	case ast.CreateUser:
		return st.Name
	case ast.DropUser:
		return st.Name
	case ast.CreateRole:
		return st.Name
	case ast.DropRole:
		return st.Name
	case ast.Grant:
		return st.Grantee
	case ast.Revoke:
		return st.Grantee
	default:
		return ""
	}
}

func workflowAuditAction(stmt ast.Stmt) string {
	switch stmt.(type) {
	case ast.CreateWorkflow:
		return security.ActionWorkflowCreate
	case ast.RunWorkflow:
		return security.ActionWorkflowRun
	case ast.AlterWorkflow:
		return security.ActionWorkflowAlter
	case ast.DropWorkflow:
		return security.ActionWorkflowDrop
	case ast.CreateTrigger:
		return security.ActionTriggerCreate
	case ast.AlterTrigger:
		return security.ActionTriggerAlter
	case ast.DropTrigger:
		return security.ActionTriggerDrop
	case ast.CreateSchedule:
		return security.ActionScheduleCreate
	case ast.AlterSchedule:
		return security.ActionScheduleAlter
	case ast.DropSchedule:
		return security.ActionScheduleDrop
	case ast.CreateResourceGroup:
		return security.ActionResourceGroupCreate
	case ast.AlterResourceGroup:
		return security.ActionResourceGroupAlter
	case ast.DropResourceGroup:
		return security.ActionResourceGroupDrop
	case ast.CancelTask:
		return security.ActionTaskCancel
	default:
		return security.ActionDDL
	}
}
