using System;
using System.Diagnostics;
using System.IO;
using System.Linq;
using FlaUI.Core;
using FlaUI.Core.AutomationElements;
using FlaUI.UIA2;

namespace SpoolSmithGui.Tests;

/// <summary>
/// Launches the real spoolsmith-gui.exe once per test class and attaches to
/// it with UIA2 (not UIA3: UIA3 has known compatibility bugs against raw
/// Win32/WinForms-style controls, which is exactly what the walk toolkit
/// creates). Each STA test owns its automation object and application lifetime
/// so COM objects and window state are not carried between test threads.
/// </summary>
public sealed class AppFixture : IDisposable
{
    public Application App { get; }
    public UIA2Automation Automation { get; }
    public Window MainWindow { get; }
    public string RepoRoot { get; }
    public string CliExePath { get; }

    /// <summary>
    /// Tests launch without the startup scan so they start deterministically.
    /// Screenshot capture opts back in, because scanning the network the PC is
    /// already on is the behaviour being shown.
    /// </summary>
    public AppFixture(bool autoScan = false, string? profilesDirectory = null, params string[] args)
    {
        RepoRoot = FindRepoRoot();
        var exePath = Path.Combine(RepoRoot, "dist", "spoolsmith-gui.exe");
        var fromEnv = Environment.GetEnvironmentVariable("SPOOLSMITH_GUI_EXE");
        if (!string.IsNullOrWhiteSpace(fromEnv))
        {
            exePath = fromEnv;
        }
        if (!File.Exists(exePath))
        {
            throw new FileNotFoundException(
                $"spoolsmith-gui.exe not found at '{exePath}'. Build it first " +
                "(go build -o dist/spoolsmith-gui.exe ./cmd/spoolsmith-gui) or set " +
                "the SPOOLSMITH_GUI_EXE environment variable to its path.",
                exePath);
        }

        // Only the Intune wizard test needs the CLI binary; resolved here (not
        // required to exist) so every other test keeps working without it.
        CliExePath = Path.Combine(RepoRoot, "dist", "spoolsmith.exe");
        var cliFromEnv = Environment.GetEnvironmentVariable("SPOOLSMITH_CLI_EXE");
        if (!string.IsNullOrWhiteSpace(cliFromEnv))
        {
            CliExePath = cliFromEnv;
        }

        var startInfo = new ProcessStartInfo(exePath)
        {
            UseShellExecute = false,
            WorkingDirectory = RepoRoot,
        };
        // Tests start deterministically without probing the operator's Wi-Fi.
        // The discovery test explicitly scans loopback unless opted in locally.
        if (!autoScan)
        {
            startInfo.Environment["SPOOLSMITH_GUI_NO_AUTOSCAN"] = "1";
        }
        if (profilesDirectory != null) startInfo.Environment["SPOOLSMITH_PROFILES_DIR"] = profilesDirectory;
        // The one-time ".ssb files open with SpoolSmith?" bar is per-user state;
        // tests and screenshots keep it out of the way.
        startInfo.Environment["SPOOLSMITH_GUI_NO_OFFER"] = "1";
        foreach (var arg in args) startInfo.ArgumentList.Add(arg);
        App = Application.Launch(startInfo);
        Automation = new UIA2Automation();
        MainWindow = GetMainWindowWithRetry();
        MoveToKnownPosition();
    }

    /// <summary>
    /// Puts the window somewhere every test can see all of it.
    ///
    /// Two things otherwise push part of it off the display, and UI Automation
    /// then correctly reports those controls as IsOffscreen — which reads as a
    /// missing button rather than a placement artifact. Observed directly: a
    /// button lookup failed late in a full run and passed when run alone.
    ///
    /// First, Windows cascades each successive new window down and to the
    /// right, and a full run launches the app once per test. Second, the
    /// default 980x740 window is simply taller than a small desktop — a CI
    /// runner at 1024x768 among them — so it is shrunk to fit when it has to
    /// be. Walk enforces the app's own minimum size, so this never shrinks
    /// past a layout the app considers valid.
    /// </summary>
    private void MoveToKnownPosition()
    {
        try
        {
            var hwnd = MainWindow.Properties.NativeWindowHandle.Value;
            var workArea = System.Windows.Forms.Screen.PrimaryScreen?.WorkingArea
                ?? new System.Drawing.Rectangle(0, 0, 1024, 768);
            var bounds = MainWindow.BoundingRectangle;
            var fits = bounds.Width > 0 && bounds.Height > 0
                && bounds.Width <= workArea.Width && bounds.Height <= workArea.Height;
            if (fits)
            {
                // Move only. Sizing from BoundingRectangle is not safe here: it
                // can be read before the window has settled, and this process is
                // not per-monitor DPI aware while the app is, so on a scaled
                // display those numbers are not the window's real pixel size.
                // Passing them back shrank the window and clipped its bottom
                // row of buttons, which surfaced as a button that could not be
                // found at all.
                SetWindowPos(hwnd, IntPtr.Zero, workArea.X, workArea.Y, 0, 0,
                    SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE);
                return;
            }
            // Too tall or wide for this desktop — a 1024x768 CI runner, say.
            // Fall back to the app's own minimum, scaled the way TruncationTests
            // already does it, rather than to a size derived from a rectangle
            // that may be in different units.
            var scale = GetDpiForWindow(hwnd) / 96.0;
            SetWindowPos(hwnd, IntPtr.Zero, workArea.X, workArea.Y,
                Math.Min((int)Math.Round(960 * scale), workArea.Width),
                Math.Min((int)Math.Round(640 * scale), workArea.Height),
                SWP_NOZORDER | SWP_NOACTIVATE);
        }
        catch
        {
            // Placement is a convenience; a failure here must not mask the
            // actual assertion the test is making.
        }
    }

    private const uint SWP_NOSIZE = 0x0001;
    private const uint SWP_NOZORDER = 0x0004;
    private const uint SWP_NOACTIVATE = 0x0010;

    [System.Runtime.InteropServices.DllImport("user32.dll")]
    private static extern uint GetDpiForWindow(IntPtr hwnd);

    [System.Runtime.InteropServices.DllImport("user32.dll", SetLastError = true)]
    [return: System.Runtime.InteropServices.MarshalAs(System.Runtime.InteropServices.UnmanagedType.Bool)]
    private static extern bool SetWindowPos(IntPtr hwnd, IntPtr insertAfter, int x, int y, int width, int height, uint flags);

    /// <summary>
    /// UIA2's AutomationElement.FromHandle can throw a transient COMException
    /// (E_FAIL from UiaNodeFromHandle) when called right as a window is being
    /// created — a documented UI-Automation startup race, not a real failure.
    /// Retrying a few times is the standard workaround.
    /// </summary>
    private Window GetMainWindowWithRetry()
    {
        Exception? last = null;
        for (var attempt = 0; attempt < 5; attempt++)
        {
            try
            {
                var window = App.GetMainWindow(Automation, TimeSpan.FromSeconds(15))
                    ?? throw new TimeoutException("SpoolSmith's main window never appeared.");
                // GetMainWindow can hand back the window before it is fully
                // realized, when it still reports an empty title. Observed
                // intermittently: a run failed asserting the title was
                // "SpoolSmith" and saw "" instead. Wait for the real title
                // rather than letting the race surface as a content assertion.
                var ready = DateTime.UtcNow.AddSeconds(10);
                while (string.IsNullOrEmpty(window.Title) && DateTime.UtcNow < ready)
                {
                    System.Threading.Thread.Sleep(100);
                }
                return window;
            }
            catch (System.Runtime.InteropServices.COMException ex)
            {
                last = ex;
                System.Threading.Thread.Sleep(500);
            }
        }
        throw new InvalidOperationException("Could not attach to SpoolSmith's main window after retrying.", last);
    }

    /// <summary>The sidebar's pages, in order, as their list items are named.</summary>
    public static readonly string[] Pages = { "This PC", "Add a printer" };

    /// <summary>The destinations under the sidebar's More menu.</summary>
    public static readonly string[] MorePages = { "Intune package", "Inspect", "Action log" };

    /// <summary>The More button's caption, which is also its UIA name.</summary>
    public const string MoreButton = "More  ▾";

    /// <summary>
    /// "Review" is the apply sheet. It has no sidebar entry: it opens only
    /// with something to apply, so it is reached by launching with a printer
    /// file (see the args constructor parameter) or from another page.
    /// </summary>
    public const string Review = "Review";

    /// <summary>
    /// Opens a page the way an operator would -- the sidebar for the two main
    /// pages, the More menu for the rest -- and waits until its content is
    /// visible.
    ///
    /// The sidebar is an owner-drawn native list whose items keep their text,
    /// so UIA exposes each one as a named ListItem. More opens a native popup
    /// menu, which UIA exposes as a top-level Menu of MenuItems.
    /// </summary>
    public AutomationElement GoTo(string page)
    {
        var marker = page switch
        {
            "This PC" => "thispc-list",
            "Add a printer" => "discover-results",
            Review => "‹ Back",
            "Intune package" => "Build an Intune printer app...",
            "Inspect" => "inspect-target",
            "Action log" => "log-output",
            _ => throw new ArgumentException("Unknown page", nameof(page)),
        };
        Func<bool> shown = () =>
        {
            var content = MainWindow.FindFirstDescendant(cf => cf.ByName(marker));
            return content != null && !content.IsOffscreen;
        };
        if (page == Review)
        {
            Wait(shown, "The apply sheet is not open.");
            return MainWindow;
        }
        if (Array.IndexOf(Pages, page) >= 0)
        {
            var nav = MainWindow.FindFirstDescendant(cf => cf.ByName("navigation").And(cf.ByControlType(FlaUI.Core.Definitions.ControlType.List)))
                ?? throw new InvalidOperationException("Sidebar navigation not found.");
            var item = nav.FindFirstDescendant(cf => cf.ByControlType(FlaUI.Core.Definitions.ControlType.ListItem).And(cf.ByName(page)))
                ?? throw new InvalidOperationException($"Sidebar item '{page}' not found.");
            var deadline = DateTime.UtcNow.AddSeconds(5);
            while (DateTime.UtcNow < deadline)
            {
                // Retry harmless navigation if a launch/focus transition consumed the click.
                MainWindow.SetForeground();
                item.Click();
                if (Poll(shown, 1_000)) return item;
            }
            throw new TimeoutException($"Page '{page}' did not show its content after clicking.");
        }
        for (var attempt = 0; attempt < 3; attempt++)
        {
            var menuItem = OpenMoreMenuItem(page);
            if (menuItem.Patterns.Invoke.IsSupported) menuItem.Patterns.Invoke.Pattern.Invoke();
            else menuItem.Click();
            if (Poll(shown, 3_000)) return menuItem;
        }
        throw new TimeoutException($"Page '{page}' did not open from More.");
    }

    /// <summary>
    /// Opens the More menu and returns the named item. The menu is a separate
    /// top-level window owned by the app, so it is found from the desktop.
    /// </summary>
    public AutomationElement OpenMoreMenuItem(string name)
    {
        var more = MainWindow.FindFirstDescendant(cf => cf.ByControlType(FlaUI.Core.Definitions.ControlType.Button).And(cf.ByName(MoreButton)))
            ?? throw new InvalidOperationException("The More button was not found.");
        MainWindow.SetForeground();
        more.Click();
        AutomationElement? item = null;
        Wait(() => (item = Automation.GetDesktop()
            .FindAllChildren(cf => cf.ByClassName("#32768"))
            .Select(menu => menu.FindFirstDescendant(cf => cf.ByControlType(FlaUI.Core.Definitions.ControlType.MenuItem).And(cf.ByName(name))))
            .FirstOrDefault(found => found != null)) != null, $"More menu item '{name}' not found.");
        return item!;
    }

    /// <summary>
    /// Opens More, reports whether it lists the named item, and closes it again.
    /// </summary>
    public bool MoreMenuHas(string name)
    {
        var more = MainWindow.FindFirstDescendant(cf => cf.ByControlType(FlaUI.Core.Definitions.ControlType.Button).And(cf.ByName(MoreButton)))
            ?? throw new InvalidOperationException("The More button was not found.");
        MainWindow.SetForeground();
        more.Click();
        AutomationElement[] items = Array.Empty<AutomationElement>();
        Wait(() => (items = Automation.GetDesktop()
            .FindAllChildren(cf => cf.ByClassName("#32768"))
            .SelectMany(menu => menu.FindAllDescendants(cf => cf.ByControlType(FlaUI.Core.Definitions.ControlType.MenuItem)))
            .ToArray()).Length > 0, "The More menu did not open.");
        var found = items.Any(item => item.Name == name);
        FlaUI.Core.Input.Keyboard.Press(FlaUI.Core.WindowsAPI.VirtualKeyShort.ESCAPE);
        Poll(() => !Automation.GetDesktop().FindAllChildren(cf => cf.ByClassName("#32768")).Any(), 2_000);
        return found;
    }

    private static bool Poll(Func<bool> ready, int timeoutMs)
    {
        var deadline = DateTime.UtcNow.AddMilliseconds(timeoutMs);
        while (DateTime.UtcNow < deadline)
        {
            if (ready()) return true;
            System.Threading.Thread.Sleep(100);
        }
        return false;
    }

    private static void Wait(Func<bool> ready, string failure, int timeoutMs = 15_000)
    {
        if (!Poll(ready, timeoutMs)) throw new TimeoutException(failure);
    }

    /// <summary>Absolute path to a file under the repo's fixtures/ directory.</summary>
    public string FixturePath(string name) => Path.Combine(RepoRoot, "fixtures", name);

    internal static string FindRepoRoot()
    {
        var dir = new DirectoryInfo(AppContext.BaseDirectory);
        while (dir != null && !File.Exists(Path.Combine(dir.FullName, "go.mod")))
        {
            dir = dir.Parent;
        }
        if (dir == null)
        {
            throw new InvalidOperationException(
                "Could not locate the spoolsmith repo root (no go.mod found above the test output directory).");
        }
        return dir.FullName;
    }

    public void Dispose()
    {
        try
        {
            App.Close();
        }
        catch
        {
            // Best-effort: the app may already be gone if a test crashed it.
        }
        Automation.Dispose();
        App.Dispose();
    }
}
