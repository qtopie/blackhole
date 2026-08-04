# Fluent UI Modal Dialog System Specification

## Behavioral & UI Contracts

1. **Native Dialog Prohibition Contract**:
   - Web UI MUST NOT use native `window.alert()` or `window.confirm()` dialogs anywhere.
   - All interactive confirmations and notification alerts MUST use the Fluent UI modal dialog framework (`#fluentModalDialog`).

2. **Fluent Design System Specs**:
   - **Backdrop**: Smooth translucent blur backdrop (`backdrop-filter: blur(8px)`, `background: rgba(0, 0, 0, 0.45)`).
   - **Card Container**: Rounded 12px acrylic card with depth shadow (`box-shadow: 0 16px 32px rgba(0, 0, 0, 0.28)`), smooth scale-up animation.
   - **Header & Title**: Clear Fluent typography with support for Accent/Danger icons (`⚠️`, `🗑️`, `💡`, `❌`).
   - **Actions**: Accent button for primary/confirm action, Subtle button for cancel action.
   - **Keyboard Accessibility**: `Esc` key cancels confirmation, `Enter` key triggers primary action.

3. **Async API Interface**:
   - `showFluentConfirm({ title, content, confirmText, cancelText, isDanger }): Promise<boolean>`
   - `showFluentAlert({ title, content, buttonText, icon }): Promise<void>`
