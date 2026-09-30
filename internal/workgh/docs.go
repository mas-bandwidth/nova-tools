package workgh

// The pinned field lists. Everything the tree keeps comes from here; a field
// not asked for is not captured, and SPEC-WORK-V1 section 1.3 lists both.

const issueFields = `
fragment IssueFields on Issue {
  id number url title body state stateReason createdAt updatedAt closedAt locked activeLockReason
  author{login} authorAssociation
  labels(first:100){totalCount nodes{name}}
  assignees(first:100){totalCount nodes{login}}
  milestone{number title}
  comments(first:100){totalCount pageInfo{hasNextPage endCursor} nodes{...CommentFields}}
  timelineItems(first:100,itemTypes:[CROSS_REFERENCED_EVENT]){totalCount pageInfo{hasNextPage endCursor} nodes{...RefFields}}
  closedByPullRequestsReferences(first:100,includeClosedPrs:true){totalCount pageInfo{hasNextPage endCursor} nodes{...PRFields}}
}
fragment CommentFields on IssueComment { id url body createdAt updatedAt author{login} authorAssociation }
fragment RefFields on IssueTimelineItems { ... on CrossReferencedEvent { createdAt willCloseTarget actor{login}
  source{ __typename ... on Issue{url number repository{nameWithOwner}} ... on PullRequest{url number repository{nameWithOwner}} } } }
fragment PRFields on PullRequest { number url state repository{nameWithOwner} }
`

const reposDoc = `query($org:String!,$after:String){
  rateLimit{cost remaining resetAt}
  organization(login:$org){
    repositories(first:100,after:$after,orderBy:{field:NAME,direction:ASC}){
      totalCount pageInfo{hasNextPage endCursor}
      nodes{nameWithOwner url isArchived issues{totalCount}}
    }
  }
}`

const repoDoc = `query($owner:String!,$repo:String!){
  rateLimit{cost remaining resetAt}
  repository(owner:$owner,name:$repo){nameWithOwner url isArchived issues{totalCount}}
}`

const issuesDoc = `query($owner:String!,$repo:String!,$n:Int!,$after:String){
  rateLimit{cost remaining resetAt}
  repository(owner:$owner,name:$repo){
    issues(first:$n,after:$after,orderBy:{field:CREATED_AT,direction:ASC},states:[OPEN,CLOSED]){
      totalCount pageInfo{hasNextPage endCursor}
      nodes{...IssueFields}
    }
  }
}` + issueFields

// The follow-up documents: the rest of one issue's connection after a cursor.
const commentsDoc = `query($owner:String!,$repo:String!,$number:Int!,$after:String){
  rateLimit{cost remaining resetAt}
  repository(owner:$owner,name:$repo){ issue(number:$number){
    comments(first:100,after:$after){totalCount pageInfo{hasNextPage endCursor} nodes{...CommentFields}} } }
}
fragment CommentFields on IssueComment { id url body createdAt updatedAt author{login} authorAssociation }`

const refsDoc = `query($owner:String!,$repo:String!,$number:Int!,$after:String){
  rateLimit{cost remaining resetAt}
  repository(owner:$owner,name:$repo){ issue(number:$number){
    timelineItems(first:100,after:$after,itemTypes:[CROSS_REFERENCED_EVENT]){totalCount pageInfo{hasNextPage endCursor} nodes{...RefFields}} } }
}
fragment RefFields on IssueTimelineItems { ... on CrossReferencedEvent { createdAt willCloseTarget actor{login}
  source{ __typename ... on Issue{url number repository{nameWithOwner}} ... on PullRequest{url number repository{nameWithOwner}} } } }`

const prsDoc = `query($owner:String!,$repo:String!,$number:Int!,$after:String){
  rateLimit{cost remaining resetAt}
  repository(owner:$owner,name:$repo){ issue(number:$number){
    closedByPullRequestsReferences(first:100,after:$after,includeClosedPrs:true){totalCount pageInfo{hasNextPage endCursor} nodes{...PRFields}} } }
}
fragment PRFields on PullRequest { number url state repository{nameWithOwner} }`
