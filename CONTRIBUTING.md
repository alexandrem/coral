# Contributing to Coral

Thank you for your interest in contributing to Coral! This document provides guidelines and best practices for contributing to the project.

## Table of Contents

- [Development Workflow](#development-workflow)
- [Running Tests Locally](#running-tests-locally)
- [Pull Request Process](#pull-request-process)
- [Branch Protection & Best Practices](#branch-protection--best-practices)
- [Code Review Guidelines](#code-review-guidelines)
- [Continuous Integration](#continuous-integration)

## Development Workflow

1. **Fork and Clone**: Fork the repository and clone it to your local machine
2. **Create a Branch**: Create a feature branch from `main`
   ```bash
   git checkout -b feature/your-feature-name
   ```
3. **Make Changes**: Implement your changes following our coding standards
4. **Test Locally**: Ensure all tests pass before pushing (see below)
5. **Commit**: Write clear, descriptive commit messages
6. **Push**: Push your branch to your fork
7. **Open PR**: Create a pull request against `main`

## Running Tests Locally

**Before committing any code**, ensure all checks pass locally:

```bash
# Run all tests (REQUIRED before commits per CLAUDE.md)
make test

# Run linter
make lint

# Run go vet
make vet

# Check code formatting
make fmt

# Build to ensure compilation
make build

# Run all checks at once
make all
```

### Pre-commit Checklist

- [ ] All tests pass (`make test`)
- [ ] Code is formatted (`make fmt`)
- [ ] No linting errors (`make lint`)
- [ ] No vet warnings (`make vet`)
- [ ] Code builds successfully (`make build`)
- [ ] New tests added for new features
- [ ] Documentation updated if needed

## Pull Request Process

### 1. Before Creating a PR

- Ensure your branch is up to date with `main`:
  ```bash
  git fetch origin
  git rebase origin/main
  ```
- Run all tests locally (see above)
- Review your own changes first

### 2. PR Title and Description

- **Title**: Use a clear, descriptive title
  - Good: `feat: add STUN-based NAT traversal`
  - Bad: `update code`
- **Description**: Include:
  - What changes were made and why
  - Related issue numbers (if applicable)
  - Testing performed
  - Screenshots (for UI changes)
  - Breaking changes (if any)

### 3. PR Size Guidelines

- Keep PRs focused and reasonably sized (< 400 lines preferred)
- Break large features into multiple PRs when possible
- One logical change per PR

### 4. PR Labels

Use appropriate labels:
- `bug`: Bug fixes
- `feature`: New features
- `docs`: Documentation changes
- `refactor`: Code refactoring
- `performance`: Performance improvements
- `breaking`: Breaking changes

## Branch Protection & Best Practices

### Recommended Branch Protection Rules for `main`

To prevent regressions and maintain code quality, configure the following branch protection rules in GitHub:

#### Required Settings

1. **Require Pull Request Reviews**
   - At least 1 approval required
   - Dismiss stale reviews when new commits are pushed
   - Require review from code owners (if CODEOWNERS file exists)

2. **Require Status Checks to Pass**
   - Require branches to be up to date before merging
   - Required status checks:
     - `test` - All tests must pass
     - `lint` - Linting must pass
     - `vet` - Go vet must pass
     - `format` - Code formatting must be correct
     - `build` - Build must succeed
     - `security` - Security scan must complete
     - `dependencies` - Dependency verification must pass

3. **Require Linear History**
   - Enable "Require linear history" to maintain clean git history
   - Use squash merging or rebase merging (no merge commits)

4. **Do Not Allow Force Pushes**
   - Disable force pushes to `main`
   - Protects against accidental history rewrites

5. **Require Signed Commits** (Recommended)
   - Ensure commit authenticity
   - Configure GPG signing for all commits

6. **Include Administrators**
   - Apply rules to administrators as well
   - No exceptions for maintainers

#### Configuration Example

Navigate to: **Settings > Branches > Add branch protection rule**

```
Branch name pattern: main

☑ Require a pull request before merging
  ☑ Require approvals (1)
  ☑ Dismiss stale pull request approvals when new commits are pushed
  ☑ Require review from Code Owners

☑ Require status checks to pass before merging
  ☑ Require branches to be up to date before merging
  Status checks that are required:
    - test
    - lint
    - vet
    - format
    - build
    - security
    - dependencies

☑ Require conversation resolution before merging
☑ Require linear history
☑ Do not allow bypassing the above settings
☑ Restrict who can push to matching branches (optional)
```

### Additional Best Practices

#### 1. Automated Testing on Every Commit

Our CI pipeline (`.github/workflows/ci.yml`) automatically runs on:
- Every push to any branch
- Every pull request to `main`

This ensures:
- Regressions are caught early
- All contributors run the same checks
- Code quality remains consistent

#### 2. Merge Strategy

Recommended merge strategies (choose one and enforce):

- **Squash and Merge** (Recommended)
  - Keeps `main` history clean
  - One commit per PR
  - Easier to revert if needed

- **Rebase and Merge**
  - Preserves commit history
  - Linear history
  - Good for well-structured commits

- **Avoid Merge Commits**
  - Creates cluttered history
  - Harder to track changes

#### 3. Draft PRs for Work in Progress

- Use Draft PRs for incomplete work
- Allows CI to run without requesting reviews
- Mark as "Ready for Review" when complete

#### 4. Keep PRs Up to Date

- Regularly rebase on `main` to catch integration issues early
- GitHub can auto-update branches if configured

#### 5. Dependency Updates

- Review dependency updates carefully
- Run full test suite
- Check for breaking changes in changelogs
- Consider security implications

## Code Review Guidelines

### For Authors

- Respond to all comments
- Be open to feedback
- Explain your reasoning when disagreeing
- Update PR based on feedback promptly
- Mark conversations as resolved after addressing

### For Reviewers

- Review within 1-2 business days
- Be respectful and constructive
- Explain the "why" behind suggestions
- Approve when satisfied, don't block on minor nits
- Focus on:
  - Correctness
  - Security implications
  - Performance concerns
  - Code maintainability
  - Test coverage
  - Documentation

### Review Checklist

- [ ] Code follows Go conventions (Effective Go, Go Doc Comments)
- [ ] Tests are comprehensive and pass
- [ ] No security vulnerabilities introduced
- [ ] Error handling is appropriate
- [ ] Comments end with a period (per CLAUDE.md)
- [ ] Performance impact is acceptable
- [ ] Breaking changes are documented
- [ ] Public APIs are well-documented

## Continuous Integration

### CI Pipeline

Our GitHub Actions workflow runs the following jobs:

1. **Test**: Runs `make test` to execute all unit tests
2. **Lint**: Runs `golangci-lint` to catch code quality issues
3. **Vet**: Runs `go vet` to identify suspicious code
4. **Format**: Checks code formatting with `goimports`
5. **Build**: Ensures the project compiles successfully
6. **Security**: Runs Gosec security scanner
7. **Dependencies**: Verifies go.mod and go.sum are tidy

All jobs must pass before a PR can be merged.

### Local CI Simulation

To simulate CI locally before pushing:

```bash
# Run all checks
make test && make lint && make vet && make build

# Check formatting
goimports -l .

# Verify dependencies
go mod verify && go mod tidy
```

### Handling CI Failures

If CI fails:

1. Read the error message carefully
2. Reproduce locally using the same commands
3. Fix the issue
4. Re-run tests locally
5. Push the fix
6. Verify CI passes

## Getting Help

- Review existing documentation in `/docs`
- Check open and closed issues for similar problems
- Ask questions in PR comments
- Follow coding standards in `CLAUDE.md`

## License

By contributing, you agree that your contributions will be licensed under the same license as the project.

---

Thank you for contributing to Coral! 🎉
