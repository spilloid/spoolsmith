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
        _fixture = new AppFixture();
        _testDirectory = Path.Combine(_fixture.RepoRoot, "dist", "gui-test-data", Guid.NewGuid().ToString("N"));
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
        Assert.False(Find(_fixture.MainWindow, "discover-cidr").IsOffscreen);
        var visibleTabs = _fixture.MainWindow.FindAllDescendants(cf => cf.ByControlType(ControlType.TabItem))
            .Where(tab => !tab.IsOffscreen).Select(tab => tab.Name).ToArray();
        Assert.Equal(new[] { "Find a printer", "Add printer", "Saved printers", "Review and apply", "Tools" }, visibleTabs);
    }

    [StaFact]
    public void Scan_completes_and_populates_discovery_results()
    {
        _fixture.SelectTab("Find a printer");
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
    public void Updating_settings_requires_a_profile_and_cannot_enable_execution()
    {
        _fixture.SelectTab("Review and apply");
        var execute = FindButton(_fixture.MainWindow, "Add printer...");
        Assert.False(execute.IsEnabled);
        var configure = Find(_fixture.MainWindow, "Update settings").AsRadioButton();
        configure.Click();
        Assert.True(configure.IsChecked);
        // These constraints live in the advanced panel, which is hidden until
        // asked for, and a hidden control is absent from the automation tree.
        _fixture.MainWindow.FindFirstDescendant(cf => cf.ByName("Advanced options"))!.AsCheckBox().Click();
        WaitUntil(() => !IsHidden("Use a saved profile"), "Advanced options did not open.");
        Assert.True(Find(_fixture.MainWindow, "Use a saved profile").AsCheckBox().IsChecked);
        Assert.False(Find(_fixture.MainWindow, "Use a saved profile").IsEnabled);
        Assert.False(Find(_fixture.MainWindow, "mutate-force-family").IsEnabled);
        var output = Find(_fixture.MainWindow, "mutate-output").AsTextBox();
        FindButton(_fixture.MainWindow, "Preview changes").Invoke();
        WaitForText(output, t => t.Contains("profile", StringComparison.OrdinalIgnoreCase));
        Assert.False(FindButton(_fixture.MainWindow, "Update settings...").IsEnabled);
    }

    /// <summary>
    /// walk's SetVisible is a no-op while a control's tab page is hidden — the
    /// control already reports itself invisible through its hidden ancestor —
    /// so a panel hidden during startup silently reappears the first time its
    /// page is opened. Both optional panels were doing exactly that, showing an
    /// empty editor and the advanced options to every operator.
    /// </summary>
    [StaFact]
    public void Optional_panels_stay_hidden_until_they_are_asked_for()
    {
        _fixture.SelectTab("Saved printers");
        Assert.True(IsHidden("Edit saved settings"), "The saved-settings editor opened without being asked for.");

        _fixture.SelectTab("Review and apply");
        Assert.True(IsHidden("Preview only (disable installation)"), "Advanced options opened without being asked for.");

        _fixture.MainWindow.FindFirstDescendant(cf => cf.ByName("Advanced options"))!.AsCheckBox().Click();
        WaitUntil(() => !IsHidden("Preview only (disable installation)"), "Advanced options did not open when requested.");
    }

    /// <summary>
    /// A control walk has hidden leaves the automation tree entirely, so being
    /// absent is the primary signal. IsOffscreen additionally catches a control
    /// that is present but clipped out of view. Native static labels do not all
    /// publish IsOffscreen through UIA2; for those, presence is the answer.
    /// </summary>
    private bool IsHidden(string accessibleName)
    {
        var element = _fixture.MainWindow.FindFirstDescendant(cf => cf.ByName(accessibleName));
        if (element == null) return true;
        try
        {
            return element.IsOffscreen;
        }
        catch (FlaUI.Core.Exceptions.PropertyNotSupportedException)
        {
            return false;
        }
    }

    [StaFact]
    public void Missing_saved_printer_folder_explains_how_to_get_started()
    {
        _fixture.SelectTab("Saved printers");
        var missingDirectory = Path.Combine(_testDirectory, "not-created");
        SetText(Find(_fixture.MainWindow, "profiles-dir").AsTextBox(), missingDirectory);
        FindButton(_fixture.MainWindow, "Refresh").Invoke();

        var text = WaitForText(Find(_fixture.MainWindow, "profiles-output").AsTextBox(),
            t => t.Contains("No saved printers", StringComparison.OrdinalIgnoreCase));
        Assert.Contains("Add printer", text, StringComparison.OrdinalIgnoreCase);
        Assert.Empty(Find(_fixture.MainWindow, "profiles-list").AsListBox().Items);
        Assert.False(FindButton(_fixture.MainWindow, "Set up printer").IsEnabled);
        Assert.False(FindButton(_fixture.MainWindow, "Update printer").IsEnabled);
        Assert.False(FindButton(_fixture.MainWindow, "Remove printer").IsEnabled);
        Assert.False(Directory.Exists(missingDirectory));
    }

    [StaTheory]
    [InlineData("Set up printer", "Add printer", "Add printer...")]
    [InlineData("Update printer", "Update settings", "Update settings...")]
    [InlineData("Remove printer", "Remove printer", "Remove printer...")]
    public void Saved_printer_selection_shows_details_and_hands_off_to_review(string action, string mode, string applyCaption)
    {
        var profilePath = CreateSavedPrinter();
        _fixture.SelectTab("Saved printers");
        SetText(Find(_fixture.MainWindow, "profiles-dir").AsTextBox(), _testDirectory);
        FindButton(_fixture.MainWindow, "Refresh").Invoke();

        var list = Find(_fixture.MainWindow, "profiles-list").AsListBox();
        Assert.Single(list.Items);
        list.Items[0].Click();
        var details = WaitForText(Find(_fixture.MainWindow, "profiles-output").AsTextBox(),
            t => t.Contains("Test office printer", StringComparison.Ordinal));
        Assert.Contains("Test driver", details);
        Assert.Contains("127.0.0.1", details);
        FindButton(_fixture.MainWindow, action).Invoke();

        var target = Find(_fixture.MainWindow, "mutate-target").AsTextBox();
        WaitUntil(() => !target.IsOffscreen, "Saved printer action did not open Review & apply.");
        Assert.Equal(profilePath, target.Text);
        // The saved profile is what the review acts on, shown by the field's
        // own label rather than the advanced checkbox that mirrors it.
        Assert.False(IsHidden("Profile file:"));
        // Exactly one operation may be selected. walk's SetChecked sets only the
        // control it is called on, so a handoff that forgets to clear the others
        // leaves two modes checked and the preview can run the wrong operation.
        var modes = _fixture.MainWindow.FindAllDescendants(cf => cf.ByControlType(ControlType.RadioButton))
            .Where(radio => radio.Name is "Add printer" or "Update settings" or "Remove printer")
            .ToArray();
        Assert.Equal(3, modes.Length);
        Assert.Equal(new[] { mode }, modes.Where(radio => radio.AsRadioButton().IsChecked).Select(radio => radio.Name).ToArray());
        Assert.False(FindButton(_fixture.MainWindow, applyCaption).IsEnabled);
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
