using System;
using System.IO;
using System.Linq;
using System.Threading;
using FlaUI.Core.AutomationElements;
using FlaUI.Core.Definitions;
using Xunit;

namespace SpoolSmithGui.Tests;

/// <summary>
/// End-to-end checks that a click in the GUI actually reaches the same
/// internal/* packages the CLI's own tests exercise, using only fast,
/// deterministic fixture and loopback paths. Navigation, profile selection,
/// and previews never confirm an install or mutate Windows printers.
/// </summary>
public sealed class FunctionalTests : IDisposable
{
    private readonly AppFixture _fixture;
    private readonly string _testDirectory;

    public FunctionalTests()
    {
        _testDirectory = Path.Combine(Path.GetTempPath(), "spoolsmith-gui-test-" + Guid.NewGuid().ToString("N"));
        _fixture = new AppFixture(profilesDirectory: _testDirectory);
    }

    public void Dispose()
    {
        _fixture.Dispose();
        if (Directory.Exists(_testDirectory)) Directory.Delete(_testDirectory, recursive: true);
    }

    [StaFact]
    public void MainWindow_launches_with_expected_title()
    {
        Assert.Equal("SpoolSmith", _fixture.MainWindow.Title);
        Assert.False(Find(_fixture.MainWindow, "thispc-list").IsOffscreen);
        var visibleTabs = _fixture.MainWindow.FindAllDescendants(cf => cf.ByControlType(ControlType.TabItem))
            .Where(tab => !tab.IsOffscreen).Select(tab => tab.Name).ToArray();
        Assert.Equal(new[] { "This PC", "Add a printer", "Review and apply", "Tools" }, visibleTabs);
    }

    [StaFact]
    public void Scan_completes_and_populates_discovery_results()
    {
        _fixture.SelectTab("Add a printer");
        // CI only probes loopback. A local operator can opt into a real subnet
        // and require a known candidate to appear in the native results list.
        var cidr = Environment.GetEnvironmentVariable("SPOOLSMITH_DISCOVERY_CIDR") ?? "127.0.0.1/32";
        var expectedIP = Environment.GetEnvironmentVariable("SPOOLSMITH_DISCOVERY_EXPECT_IP");
        SetText(Find(_fixture.MainWindow, "discover-cidr").AsTextBox(), cidr);
        var scan = FindButton(_fixture.MainWindow, "Scan");
        scan.Invoke();
        var output = Find(_fixture.MainWindow, "discover-output").AsTextBox();
        // The results pane states the outcome in plain language; the raw
        // scan JSON moved behind "Scan details", which must become available.
        var text = WaitForText(output, t => t.Contains("after checking"), 150_000);
        Assert.Contains(cidr, text);
        Assert.True(scan.IsEnabled);
        Assert.False(FindButton(_fixture.MainWindow, "Cancel scan").IsEnabled);
        Assert.True(FindButton(_fixture.MainWindow, "Scan details").IsEnabled);
        if (!string.IsNullOrWhiteSpace(expectedIP))
        {
            var list = Find(_fixture.MainWindow, "discover-results").AsListBox();
            Assert.Contains(list.Items, item => item.Text.Contains(expectedIP));
        }
    }

    [StaFact]
    public void Catalog_list_families_populates_output()
    {
        _fixture.SelectTab("Catalog");

        var button = FindButton(_fixture.MainWindow, "List families");
        var output = Find(_fixture.MainWindow, "catalog-output").AsTextBox();

        button.Invoke();

        var text = WaitForText(output, t => t.Contains("hp-laserjet", StringComparison.OrdinalIgnoreCase));
        Assert.Contains("brother-hl-l2xxx", text, StringComparison.OrdinalIgnoreCase);
    }

    [StaFact]
    public void Inspect_fixture_file_resolves_a_family()
    {
        _fixture.SelectTab("Inspect");

        var target = Find(_fixture.MainWindow, "inspect-target").AsTextBox();
        var button = FindButton(_fixture.MainWindow, "Inspect");
        var output = Find(_fixture.MainWindow, "inspect-output").AsTextBox();

        var fixturePath = _fixture.FixturePath("hp-laserjet-m404-synthetic.json");
        Assert.True(File.Exists(fixturePath), $"expected fixture at {fixturePath}");

        SetText(target, fixturePath);
        Assert.Equal(fixturePath, target.Text);
        button.Invoke();

        string text;
        try { text = WaitForText(output, t => t.Contains("confidence", StringComparison.OrdinalIgnoreCase)); }
        catch (TimeoutException ex)
        {
            _fixture.MainWindow.CaptureToFile(Path.Combine(_fixture.RepoRoot, "dist", "inspect-failure.png"));
            var controls = _fixture.MainWindow.FindAllDescendants();
            throw new TimeoutException(ex.Message + "\nButton: " + button.BoundingRectangle + " enabled=" + button.IsEnabled + "\nControls: " + string.Join(" | ", System.Linq.Enumerable.Select(controls, c => c.ControlType + ":" + c.Name)), ex);
        }
        Assert.Contains("hp-laserjet-m4xx", text, StringComparison.OrdinalIgnoreCase);
    }

    [StaFact]
    public void Empty_review_cannot_enable_execution()
    {
        _fixture.SelectTab("Review and apply");
        Assert.False(FindButton(_fixture.MainWindow, "Apply...").IsEnabled);
        Assert.False(FindButton(_fixture.MainWindow, "Preview changes").IsEnabled);
    }

    [StaFact]
    public void Direct_IP_opens_settings_on_the_same_page()
    {
        _fixture.SelectTab("Add a printer");
        SetText(Find(_fixture.MainWindow, "discover-cidr").AsTextBox(), "192.0.2.40");
        FindButton(_fixture.MainWindow, "Use IP directly").Invoke();
        WaitUntil(() => !IsHidden("capture-target"), "Settings did not open.");
        Assert.Equal("192.0.2.40", Find(_fixture.MainWindow, "capture-target").AsTextBox().Text);
        Assert.False(FindButton(_fixture.MainWindow, "Save and review").IsOffscreen);
    }

    [StaFact]
    public void Optional_panels_stay_hidden_until_requested()
    {
        _fixture.SelectTab("Add a printer");
        Assert.True(IsHidden("capture-target"));
        _fixture.SelectTab("Review and apply");
        Assert.True(IsHidden("Preview only (never apply)"));
        Find(_fixture.MainWindow, "More options").AsCheckBox().Click();
        WaitUntil(() => !IsHidden("Preview only (never apply)"), "More options did not open.");
        Assert.True(IsHidden("Also remove the driver, if nothing else uses it"));
        Assert.True(IsHidden("Do not contact the printer (its identity will not be checked)"));
    }

    private bool IsHidden(string accessibleName)
    {
        var element = _fixture.MainWindow.FindFirstDescendant(cf => cf.ByName(accessibleName));
        return element == null || element.IsOffscreen;
    }

    private Window OpenSavedSetups()
    {
        _fixture.SelectTab("Add a printer");
        FindButton(_fixture.MainWindow, "Open a saved setup...").Invoke();
        Window? dialog = null;
        WaitUntil(() => (dialog = _fixture.App.GetAllTopLevelWindows(_fixture.Automation)
            .FirstOrDefault(w => w.Title == "Saved printer setups")) != null, "Saved setups did not open.");
        return dialog!;
    }

    [StaFact]
    public void Empty_saved_folder_exposes_import_without_creating_files()
    {
        var dialog = OpenSavedSetups();
        Assert.Contains("No saved setups", Find(dialog, "saved-detail").AsTextBox().Text);
        Assert.False(FindButton(dialog, "Set up this printer").IsEnabled);
        Assert.True(FindButton(dialog, "Import all JSON...").IsEnabled);
        Assert.True(FindButton(dialog, "Export all JSON...").IsEnabled);
        FindButton(dialog, "Close").Invoke();
        Assert.False(Directory.Exists(_testDirectory));
    }

    [StaTheory]
    [InlineData("Set up this printer", "Add printer...")]
    [InlineData("Update to match", "Update printer...")]
    [InlineData("Remove...", "Remove printer...")]
    public void Saved_setup_hands_the_named_operation_to_review(string action, string applyCaption)
    {
        CreateSavedPrinter();
        var dialog = OpenSavedSetups();
        Assert.Contains("Test office printer", Find(dialog, "saved-detail").AsTextBox().Text);
        FindButton(dialog, action).Invoke();
        WaitUntil(() => !IsHidden("review-summary"), "Review did not open.");
        Assert.False(FindButton(_fixture.MainWindow, applyCaption).IsEnabled);
        Assert.True(FindButton(_fixture.MainWindow, "Preview changes").IsEnabled);
    }

    private string CreateSavedPrinter()
    {
        Directory.CreateDirectory(_testDirectory);
        var path = Path.Combine(_testDirectory, "office-printer.json");
        File.WriteAllText(path, """
            {
              "version": 1,
              "target": "127.0.0.1",
              "printer_name": "Test office printer",
              "driver_name": "Test driver",
              "evidence": {
                "ip": "127.0.0.1",
                "http_title": "Test printer identity",
                "provenance": "captured"
              }
            }
            """);
        return path;
    }

    private static void WaitUntil(Func<bool> ready, string failure, int timeoutMs = 10_000)
    {
        var deadline = DateTime.UtcNow.AddMilliseconds(timeoutMs);
        while (DateTime.UtcNow < deadline)
        {
            if (ready()) return;
            Thread.Sleep(100);
        }
        throw new TimeoutException(failure);
    }

    private static AutomationElement Find(AutomationElement root, string accessibleName)
    {
        return root.FindFirstDescendant(cf => cf.ByName(accessibleName))
            ?? throw new InvalidOperationException($"No control with accessible name \"{accessibleName}\" was found.");
    }

    /// <summary>
    /// A plain ByName search for a caption like "Inspect" is ambiguous — the
    /// Inspect tab item shares that exact Name with the Inspect button inside
    /// it — so button lookups constrain to ControlType.Button explicitly.
    ///
    /// Polls rather than asserting on the first frame. Walk lays a tab page out
    /// after the native TCN_SELCHANGE it reacts to, and the apply button's
    /// caption is rewritten at runtime by updateReviewControls, so immediately
    /// after a navigation a button can still report IsOffscreen or carry its
    /// previous name. Observed directly: these lookups passed in isolation and
    /// failed only in a full run, where the preceding test's window teardown
    /// shifts the timing.
    /// </summary>
    private static Button FindButton(AutomationElement root, string caption, int timeoutMs = 5_000)
    {
        var deadline = DateTime.UtcNow.AddMilliseconds(timeoutMs);
        while (true)
        {
            var element = root.FindAllDescendants(cf => cf.ByControlType(ControlType.Button).And(cf.ByName(caption)))
                .FirstOrDefault(button => !button.IsOffscreen);
            if (element != null) return element.AsButton();
            if (DateTime.UtcNow >= deadline)
            {
                throw new InvalidOperationException($"No button captioned \"{caption}\" was found.");
            }
            Thread.Sleep(100);
        }
    }

    /// <summary>
    /// TextBox.Text's setter can fall back to simulated keystrokes, which
    /// (like a synthetic Click()) only land if this window happens to be
    /// foreground — confirmed directly: it silently left the real control
    /// empty, and the app's own "target required" guard fired instead of
    /// running Inspect. ValuePattern.SetValue drives the control directly
    /// through UI Automation regardless of focus or window z-order.
    /// </summary>
    private static void SetText(TextBox box, string value)
    {
        var pattern = box.Patterns.Value.PatternOrDefault
            ?? throw new InvalidOperationException("Control does not support ValuePattern.");
        pattern.SetValue(value);
    }

    /// <summary>
    /// GUI actions that call into probe/install run on a goroutine and land
    /// via walk.Synchronize, so the TextEdit updates a beat after Invoke()
    /// returns. Poll instead of sleeping a fixed amount.
    /// </summary>
    private static string WaitForText(TextBox box, Func<string, bool> ready, int timeoutMs = 10_000)
    {
        var deadline = DateTime.UtcNow.AddMilliseconds(timeoutMs);
        string last = box.Text ?? string.Empty;
        while (DateTime.UtcNow < deadline)
        {
            last = box.Text ?? string.Empty;
            if (ready(last))
            {
                return last;
            }
            Thread.Sleep(100);
        }
        throw new TimeoutException($"Timed out waiting for expected output. Last seen text:\n{last}");
    }
}
