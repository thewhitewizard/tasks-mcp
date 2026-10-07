# Tasks MCP

An MCP server that gives an AI assistant a simple task and project manager for one user, persisted in a JSON file.

## Language

**Task**:
A single thing to do, identified by its `id` (`t_…`). Has a Status, a Priority and optionally a Due date, a Project, Tags and Notes.
_Avoid_: Todo, item, ticket (reserved for GitHub issues of this repo)

**Project**:
A named group of Tasks, identified by its `id` (`p_…`). Names are unique, compared without regard to case. A Project is never deleted or archived in v1.
_Avoid_: List, folder, category

**Status**:
Where a Task stands: `todo`, `doing` or `done`. Never empty.
_Avoid_: State, phase

**Priority**:
How urgent a Task is: `low`, `normal` or `high`. Never empty.
_Avoid_: Importance, severity

**Due date**:
The day (`YYYY-MM-DD`, no time of day) by which a Task should be done. A Task without one sorts after those that have one.
_Avoid_: Deadline, due time

**Completion**:
A Task becoming `done`. Sets `completed_at`; any other Status clears it. Completing a Task that is already `done` changes nothing.
_Avoid_: Closing, finishing

**Tag**:
A short lowercase label on a Task. Compared without regard to case.
_Avoid_: Label (reserved for GitHub issue labels)

**Data file**:
The single JSON file holding all Projects and Tasks. Its path comes only from the configuration, never from a tool.
_Avoid_: Database, store file

**Store**:
The seam through which tools read and write Projects and Tasks. The v1 implementation is the Data file; a database may replace it.
_Avoid_: Repository, DAO

**Lock**:
The `.lock` file next to the Data file that serialises writers across processes. It is Stale after a fixed 30 seconds and may then be taken over.
_Avoid_: Mutex (that is in-process only)
