package organization

const (
	Supermanager   = "supermanager"
	Manager        = "admin" // Preserve the existing Manager API identifier.
	ContentOfficer = "content_officer"
	Developer      = "developer"
	MemberRole     = "member"
)

func ValidRole(role string) bool {
	return role == Supermanager || role == Manager || role == ContentOfficer || role == Developer || role == MemberRole
}
func Manage(role string) bool {
	return role == "system_admin" || role == Supermanager || role == Manager
}
func FullControl(role string) bool { return role == "system_admin" || role == Supermanager }
func Statistics(role string) bool {
	return Manage(role) || role == ContentOfficer || role == "client_manager"
}
func FullContent(role string) bool { return FullControl(role) || role == ContentOfficer }
func OwnContent(role string) bool {
	return FullContent(role) || role == Developer || role == MemberRole
}
func MutateMember(actorRole, previous, next string) bool {
	if FullControl(actorRole) {
		return true
	}
	return actorRole == Manager && (previous == Developer || previous == MemberRole) && (next == Developer || next == MemberRole)
}
func IssueKey(actorRole, actor, recipientRole, recipient string) bool {
	if FullControl(actorRole) {
		return true
	}
	if actorRole == Manager {
		return actor == recipient || recipientRole == Developer || recipientRole == MemberRole
	}
	return actorRole == Developer && (actor == recipient || recipientRole == MemberRole)
}
