# Contributing to Cairn

Thank you for your interest in contributing to Cairn! This guide will help you set up your local development environment, run tests, and understand our coding standards.

---

## 🛠️ Local Development Setup

To build and test Cairn locally, follow these steps:

1. **Clone the Repository**:
   ```bash
   git clone https://github.com/Yumekaz/Cairn.git
   cd Cairn
   ```
2. **Install Go & Python**:
   - Ensure Go **1.26.x** (matches `go.mod`) and Python v3.10+ are available on your system.
3. **Build the Project**:
   ```bash
   go build -o bin/cairn ./cmd/cairn
   go build -o bin/cairnd ./cmd/cairnd
   ```

---

## 🧪 Running Tests

Always ensure the full test suite builds and executes successfully before opening a pull request.

### Run All Unit and Integration Tests
```bash
go test -v ./...
```

### Run Tests Without Cache
```bash
go test -count=1 -v ./...
```

### Run Specific Test Cases
```bash
go test -v ./tests/integration/... -run TestReliabilityHardening
```

---

## ✍️ Coding Standards & Guidelines

- **Go Formatting**: Run `go fmt ./...` to auto-format files before staging them.
- **Error Handling**: Follow standard Go idiom guidelines. Wrap errors with clear context if bubbling them up.
- **Documentation**: If you change any API fields or CLI flags, update the relevant documentation under `docs/`.
- **Comments & Docstrings**: Maintain code clarity by keeping docstrings and inline comments updated.

---

## 📥 Submitting Pull Requests

1. Create a descriptive feature branch:
   ```bash
   git checkout -b feat/your-awesome-feature
   ```
2. Stage and commit your changes with clear, structured messages:
   ```bash
   git commit -m "feat: Add custom adapter support"
   ```
3. Push to your fork and open a Pull Request against `main`. Ensure all integration checks pass.
