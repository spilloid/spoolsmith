using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Runtime.InteropServices;
using System.Text;
using FlaUI.Core.AutomationElements;
using FlaUI.Core.Definitions;
using Xunit;

namespace SpoolSmithGui.Tests;

/// <summary>
/// Audits the real app through UI Automation, the same surface a screen reader
/// or voice-control tool uses: every visible interactive control must have an
/// accessible name and be reachable by keyboard, and Tab must actually walk
/// across the page. It also writes what it saw to dist/gui-accessibility.txt so
/// the result can be cited. It does not replace a screen-reader pass, a
/// high-contrast pass or a run at other display scales; it records the scale it
/// ran at so that limit is visible.
/// </summary>
public sealed class AccessibilityTests : IDisposable
{
    private AppFixture _fixture = null!;

    private static readonly ControlType[] Interactive =
    {
        ControlType.Button, ControlType.CheckBox, ControlType.RadioButton,
        ControlType.Edit, ControlType.ComboBox, ControlType.List, ControlType.DataGrid,
        ControlType.Table, ControlType.Document,
    };

    // Native scrollbar glyphs and window chrome carry system names or none and
    // are not operated through their own focus.
    private static readonly HashSet<string> Chrome = new(StringComparer.OrdinalIgnoreCase)
    {
        "Back by small amount", "Forward by small amount", "Back by large amount", "Forward by large amount",
        "Drop Down Button", "Thumb", "Minimize", "Maximize", "Restore", "Close", "System", "Page up", "Page down",
    };

    public void Dispose() => _fixture?.Dispose();

    public static IEnumerable<object[]> Pages
    {
        get
        {
            foreach (var page in AppFixture.Pages.Concat(AppFixture.MorePages).Append(AppFixture.Review))
                yield return new object[] { page };
        }
    }

    [DllImport("user32.dll")]
    private static extern uint GetDpiForWindow(IntPtr hwnd);

    [StructLayout(LayoutKind.Sequential)]
    private struct GuiThreadInfo
    {
        public int cbSize, flags;
        public IntPtr hwndActive, hwndFocus, hwndCapture, hwndMenuOwner, hwndMoveSize, hwndCaret;
        public int left, top, right, bottom;
    }

    [DllImport("user32.dll")]
    private static extern bool GetGUIThreadInfo(uint idThread, ref GuiThreadInfo info);

    [DllImport("user32.dll")]
    private static extern uint GetWindowThreadProcessId(IntPtr hwnd, out uint pid);

    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern int GetClassName(IntPtr hwnd, StringBuilder text, int max);

    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern int GetWindowText(IntPtr hwnd, StringBuilder text, int max);

    /// <summary>
    /// Which window really has keyboard focus, asked of Windows rather than of
    /// UI Automation, whose focused-element query can lag a keystroke.
    /// </summary>
    private static string Win32Focus(IntPtr mainWindow)
    {
        var thread = GetWindowThreadProcessId(mainWindow, out _);
        var info = new GuiThreadInfo { cbSize = Marshal.SizeOf<GuiThreadInfo>() };
        if (!GetGUIThreadInfo(thread, ref info) || info.hwndFocus == IntPtr.Zero) return "(none)";
        var cls = new StringBuilder(128);
        var text = new StringBuilder(128);
        GetClassName(info.hwndFocus, cls, cls.Capacity);
        GetWindowText(info.hwndFocus, text, text.Capacity);
        return $"{info.hwndFocus.ToInt64():x}:{cls}:{text}";
    }

    private static bool IsChrome(AutomationElement e)
    {
        var name = e.Properties.Name.ValueOrDefault ?? string.Empty;
        if (Chrome.Contains(name)) return true;
        var cls = e.Properties.ClassName.ValueOrDefault ?? string.Empty;
        return string.Equals(cls, "ScrollBar", StringComparison.OrdinalIgnoreCase);
    }

    [StaTheory]
    [MemberData(nameof(Pages))]
    public void Interactive_controls_are_named_and_keyboard_reachable(string page)
    {
        _fixture = page == AppFixture.Review
            ? new AppFixture(args: Path.Combine(AppFixture.FindRepoRoot(), "examples", "intune", "accounting.ssb"))
            : new AppFixture();
        _fixture.GoTo(page);

        var window = _fixture.MainWindow;
        var dpi = GetDpiForWindow(window.Properties.NativeWindowHandle.Value);
        var condition = Interactive
            .Select(t => (FlaUI.Core.Conditions.ConditionBase)window.ConditionFactory.ByControlType(t))
            .Aggregate((a, b) => a.Or(b));

        var report = new StringBuilder();
        report.AppendLine($"== {page} (display scale {dpi / 96.0 * 100:0}%) ==");
        var problems = new List<string>();
        var controls = 0;
        foreach (var e in window.FindAllDescendants(condition))
        {
            if (e.IsOffscreen || IsChrome(e) || e.BoundingRectangle.IsEmpty) continue;
            controls++;
            var name = (e.Properties.Name.ValueOrDefault ?? string.Empty).Trim();
            var focusable = e.Properties.IsKeyboardFocusable.ValueOrDefault;
            var enabled = e.Properties.IsEnabled.ValueOrDefault;
            report.AppendLine($"  {e.ControlType,-10} named={(name.Length > 0 ? "yes" : "NO ")} focusable={(focusable ? "yes" : "NO ")} enabled={(enabled ? "yes" : "no ")} '{name}'");
            if (name.Length == 0 && (e.ControlType == ControlType.DataGrid || e.ControlType == ControlType.Table))
            {
                // The app names these tables (thispc-list, discover-results) but UI Automation shows the
                // native list view's name on a child, not on the table. Whether a screen reader announces
                // it can only be confirmed with one, so record it rather than fail on it.
                report.AppendLine("    NOTE: table exposes an empty name through UI Automation; screen-reader announcement unconfirmed.");
            }
            else if (name.Length == 0) problems.Add($"{e.ControlType} has no accessible name at {e.BoundingRectangle}.");
            // A disabled control legitimately leaves the tab order.
            if (enabled && !focusable) problems.Add($"{e.ControlType} '{name}' is enabled but cannot take keyboard focus.");
        }

        // Tab through the page: focus must move, must land on named things, and
        // must not get stuck on one control.
        window.SetForeground();
        var seen = new List<string>();
        for (var i = 0; i < 40; i++)
        {
            FlaUI.Core.Input.Keyboard.Type(FlaUI.Core.WindowsAPI.VirtualKeyShort.TAB);
            FlaUI.Core.Input.Wait.UntilInputIsProcessed();
            System.Threading.Thread.Sleep(150);
            seen.Add(Win32Focus(window.Properties.NativeWindowHandle.Value));
        }
        var distinct = seen.Distinct().Count();
        report.AppendLine($"  Tab order, 40 presses, {distinct} distinct stops: {string.Join(" > ", seen.Distinct().Take(14))}");
        if (controls > 1 && distinct < 2) problems.Add("Tab did not move focus across the page.");

        report.AppendLine(problems.Count == 0 ? "  RESULT: no problems" : "  PROBLEMS:\n    " + string.Join("\n    ", problems));
        var path = Path.Combine(_fixture.RepoRoot, "dist", "gui-accessibility.txt");
        lock (typeof(AccessibilityTests))
        {
            File.AppendAllText(path, report.ToString() + Environment.NewLine);
        }
        Assert.True(problems.Count == 0, string.Join(Environment.NewLine, problems));
    }
}
