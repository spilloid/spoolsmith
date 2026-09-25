using System;
using System.IO;
using System.Linq;
using FlaUI.Core.AutomationElements;
using FlaUI.Core.Definitions;
using Xunit;

namespace SpoolSmithGui.Tests;

/// <summary>
/// Opening a printer file -- the way Explorer does when a .ssb is
/// double-clicked -- lands on the apply sheet, previewed and waiting for one
/// confirmation. Nothing here confirms it.
/// </summary>
public sealed class SheetTests : IDisposable
{
    private readonly AppFixture _fixture;

    public SheetTests()
    {
        var example = Path.Combine(AppFixture.FindRepoRoot(), "examples", "intune", "accounting.ssb");
        _fixture = new AppFixture(profilesDirectory: null, args: example);
    }

    public void Dispose() => _fixture.Dispose();

    [StaFact]
    public void A_printer_file_argument_opens_the_apply_sheet()
    {
        _fixture.GoTo(AppFixture.Review);
        var window = _fixture.MainWindow;
        Assert.Contains(window.FindAllDescendants(cf => cf.ByControlType(ControlType.Text)),
            label => label.Name == "Example — Accounting Copier" && !label.IsOffscreen);
        // The file carries no driver, and the sheet says so before any step.
        Assert.Contains(window.FindAllDescendants(cf => cf.ByControlType(ControlType.Text)),
            label => label.Name.StartsWith("Settings only:", StringComparison.Ordinal) && !label.IsOffscreen);
        // The preview prepares itself and shows the plan as steps.
        FunctionalTests.WaitUntil(() => window.FindAllDescendants(cf => cf.ByControlType(ControlType.Text))
            .Any(label => label.Name.Contains("Create printer \"Example — Accounting Copier\"", StringComparison.Ordinal) && !label.IsOffscreen),
            "The sheet never showed its steps.", 60_000);
        var primary = window.FindAllDescendants(cf => cf.ByControlType(ControlType.Button))
            .FirstOrDefault(b => !b.IsOffscreen && (b.Name == "Install" || b.Name == "Install as administrator..."));
        Assert.NotNull(primary);
        // Details holds the full transcript.
        window.FindFirstDescendant(cf => cf.ByName("Details"))!.AsCheckBox().Click();
        var transcript = FunctionalTests.WaitForText(FunctionalTests.Find(window, "mutate-output").AsTextBox(),
            t => t.Contains("Install plan") || t.Contains("Unable to continue"), 30_000);
        Assert.Contains("RAW9100-192.0.2.40", transcript);
    }

    [StaFact]
    public void Back_leaves_the_sheet_and_the_sidebar_still_navigates()
    {
        _fixture.GoTo(AppFixture.Review);
        FunctionalTests.FindButton(_fixture.MainWindow, "‹ Back").Invoke();
        _fixture.GoTo("This PC");
        _fixture.GoTo("Add a printer");
    }
}

/// <summary>
/// Printer files copied in Explorer and pasted into the window open the same
/// sheet as a double-click.
/// </summary>
public sealed class PasteTests : IDisposable
{
    private readonly AppFixture _fixture = new();

    public void Dispose() => _fixture.Dispose();

    [StaFact]
    public void Pasting_a_printer_file_copied_in_Explorer_opens_the_apply_sheet()
    {
        var example = Path.Combine(AppFixture.FindRepoRoot(), "examples", "intune", "accounting.ssb");
        // Explorer's Copy puts a file-drop list on the clipboard.
        System.Windows.Forms.Clipboard.SetFileDropList(new System.Collections.Specialized.StringCollection { example });
        _fixture.MainWindow.SetForeground();
        FunctionalTests.WaitUntil(() => _fixture.MainWindow.Properties.HasKeyboardFocus.ValueOrDefault
            || _fixture.MainWindow.FindAllDescendants().Any(e => e.Properties.HasKeyboardFocus.ValueOrDefault), "The window never took focus.");
        FlaUI.Core.Input.Keyboard.TypeSimultaneously(FlaUI.Core.WindowsAPI.VirtualKeyShort.CONTROL, FlaUI.Core.WindowsAPI.VirtualKeyShort.KEY_V);
        _fixture.GoTo(AppFixture.Review);
        Assert.Contains(_fixture.MainWindow.FindAllDescendants(cf => cf.ByControlType(ControlType.Text)),
            label => label.Name == "Example — Accounting Copier" && !label.IsOffscreen);
    }
}
